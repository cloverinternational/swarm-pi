package storage

// pooled_loader.go — parallel, zero-alloc conversation metadata loading.
//
// Key techniques:
//  1. buger/jsonparser — zero-allocation field extraction via EachKey.
//     For meta queries we extract only id/created_at/updated_at/workspace_path
//     and the first element of "messages" without allocating a full struct.
//  2. sync.Pool of reusable read buffers — eliminates per-file heap allocs.
//  3. ants goroutine pool — parallel I/O across workspace directories.
//  4. Filter.WorkspacePath — scan exactly ONE directory for the current CWD.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	jp "github.com/buger/jsonparser"
	"github.com/panjf2000/ants/v2"
)

// ── Buffer pool ───────────────────────────────────────────────────────────────

var bufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 128*1024)
		return &b
	},
}

func getBuf() *[]byte { return bufPool.Get().(*[]byte) }
func putBuf(b *[]byte) {
	if cap(*b) <= 4*1024*1024 {
		*b = (*b)[:0]
		bufPool.Put(b)
	}
}

// ── JSON decoder pool ─────────────────────────────────────────────────────────

type pooledDecoder struct {
	dec *json.Decoder
	r   *bytes.Reader
}

func newPooledDecoder() *pooledDecoder {
	r := bytes.NewReader(nil)
	return &pooledDecoder{dec: json.NewDecoder(r), r: r}
}

func (p *pooledDecoder) reset(data []byte) {
	p.r.Reset(data)
	p.dec = json.NewDecoder(p.r)
}

var decoderPool = sync.Pool{New: func() any { return newPooledDecoder() }}

// ── Zero-alloc meta extraction with jsonparser ────────────────────────────────

var metaPaths = [][]string{
	{"id"},                               // 0
	{"created_at"},                       // 1
	{"updated_at"},                       // 2
	{"workspace_path"},                   // 3
	{"mode"},                             // 4
	{"status"},                           // 5
	{"compaction_state", "last_summary"}, // 6
	{"title"},                            // 7
	{"total_tokens"},                     // 8
	{"current_context_size"},             // 9
	{"total_cost_usd"},                   // 10
	{"summary", "message_count"},         // 11
	{"summary", "input_tokens"},          // 12
	{"summary", "output_tokens"},         // 13
	{"summary", "tool_call_count"},       // 14
	{"summary", "last_model"},            // 15
	{"summary", "models_used"},           // 16
	{"summary", "last_preview"},          // 17
	{"summary", "first_user_prompt"},     // 18
	{"summary", "recap"},                 // 19
	{"metadata", "custom"},               // 20
	{"metadata", "tags"},                 // 21
}

// extractMeta parses only the fields needed for the sidebar using jsonparser.
// Single O(file_size) pass, no full struct allocation.
func extractMeta(data []byte, firstMsgPreview int) (*conversation.Conversation, error) {
	c := &conversation.Conversation{}
	s := &conversation.ConversationSummary{}
	var summaryTouched bool

	jp.EachKey(data, func(idx int, value []byte, vt jp.ValueType, err error) {
		if err != nil || vt == jp.NotExist {
			return
		}
		switch idx {
		case 0:
			c.ID = string(value)
		case 1:
			c.CreatedAt, _ = time.Parse(time.RFC3339Nano, string(value))
		case 2:
			c.UpdatedAt, _ = time.Parse(time.RFC3339Nano, string(value))
		case 3:
			c.WorkspacePath = string(value)
		case 4:
			c.Mode = string(value)
		case 5:
			c.Status = conversation.Status(string(value))
		case 6:
			if len(value) > 0 {
				c.CompactionState = &conversation.CompactionState{LastSummary: string(value)}
			}
		case 7:
			c.Title = string(value)
		case 8:
			if n, perr := jp.ParseInt(value); perr == nil {
				c.TotalTokens = int(n)
			}
		case 9:
			if n, perr := jp.ParseInt(value); perr == nil {
				c.CurrentContextSize = int(n)
			}
		case 10:
			if f, perr := jp.ParseFloat(value); perr == nil {
				c.TotalCostUSD = f
			}
		case 11:
			if n, perr := jp.ParseInt(value); perr == nil {
				s.MessageCount = int(n)
				summaryTouched = true
			}
		case 12:
			if n, perr := jp.ParseInt(value); perr == nil {
				s.InputTokens = int(n)
				summaryTouched = true
			}
		case 13:
			if n, perr := jp.ParseInt(value); perr == nil {
				s.OutputTokens = int(n)
				summaryTouched = true
			}
		case 14:
			if n, perr := jp.ParseInt(value); perr == nil {
				s.ToolCallCount = int(n)
				summaryTouched = true
			}
		case 15:
			s.LastModel = string(value)
			summaryTouched = true
		case 16:
			var models []string
			jp.ArrayEach(value, func(v []byte, _ jp.ValueType, _ int, _ error) {
				models = append(models, string(v))
			})
			if len(models) > 0 {
				s.ModelsUsed = models
				summaryTouched = true
			}
		case 17:
			s.LastPreview = string(value)
			summaryTouched = true
		case 18:
			s.FirstUserPrompt = string(value)
			summaryTouched = true
		case 19:
			s.Recap = string(value)
			summaryTouched = true
		case 20:
			// metadata.custom — unmarshal the raw JSON object. This is a
			// nested object with arbitrary keys (git_branch, forked_from,
			// fork_point, compacted_from, compaction_count, custom_title,
			// workspace_path, …). Kept small by the application.
			if len(value) > 0 && vt == jp.Object {
				custom := make(map[string]any)
				if err := json.Unmarshal(value, &custom); err == nil {
					c.Metadata.Custom = custom
				}
			}
		case 21:
			if len(value) > 0 && vt == jp.Array {
				var tags []string
				jp.ArrayEach(value, func(v []byte, _ jp.ValueType, _ int, _ error) {
					tags = append(tags, string(v))
				})
				c.Metadata.Tags = tags
			}
		}
	}, metaPaths...)

	if c.ID == "" {
		return nil, fmt.Errorf("missing id field")
	}
	// The messages array is authoritative. Legacy summaries can be absent or
	// stale (including off-by-one after interrupted saves), and metadata queries
	// now hold the complete JSON specifically so trailing fields are not missed.
	// Count entries without allocating Message structs.
	if messages, valueType, _, getErr := jp.Get(data, "messages"); getErr == nil && valueType == jp.Array {
		count := 0
		jp.ArrayEach(messages, func(_ []byte, _ jp.ValueType, _ int, _ error) {
			count++
		})
		s.MessageCount = count
		summaryTouched = true
	}
	// Backfill the first-class WorkspacePath from metadata.custom for legacy
	// records so workspace-scoped history tooling can find them.
	normalizeWorkspacePath(c, "")
	if summaryTouched {
		c.Summary = s
	}

	// Extract first message only if preview requested
	if firstMsgPreview > 0 {
		if msgs, _, _, err := jp.Get(data, "messages"); err == nil && len(msgs) > 1 {
			var firstRaw []byte
			jp.ArrayEach(msgs, func(value []byte, _ jp.ValueType, _ int, _ error) {
				if firstRaw == nil {
					firstRaw = make([]byte, len(value))
					copy(firstRaw, value)
				}
			})
			if firstRaw != nil {
				msg := &conversation.Message{}
				if id, err := jp.GetString(firstRaw, "id"); err == nil {
					msg.ID = id
				}
				if role, err := jp.GetString(firstRaw, "role"); err == nil {
					msg.Role = conversation.Role(role)
				}
				if content, err := jp.GetString(firstRaw, "content"); err == nil {
					msg.Content = content
				}
				if ts, err := jp.GetString(firstRaw, "timestamp"); err == nil {
					msg.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
				}
				c.Messages = []*conversation.Message{msg}
			}
		}
	}

	return c, nil
}

// ── Package-level ants pool ───────────────────────────────────────────────────
// Singleton — created once, reused across all calls.
// 8 workers covers I/O saturation on a local SSD without excessive goroutine overhead.
var (
	_ioPool     *ants.Pool
	_ioPoolOnce sync.Once
)

func getIOPool() *ants.Pool {
	_ioPoolOnce.Do(func() {
		p, err := ants.NewPool(8, ants.WithNonblocking(false), ants.WithPreAlloc(true))
		if err != nil {
			// Pool creation failure is unexpected but non-fatal; callers check for nil.
			return
		}
		_ioPool = p
	})
	return _ioPool
}

// ── result carrier ────────────────────────────────────────────────────────────

type loadResult struct {
	conv *conversation.Conversation
	err  error
}

// ── Metadata cache ────────────────────────────────────────────────────────────
//
// Metadata queries (ExcludeMessages / FirstMessagePreview) previously re-read
// EVERY conversation file on EVERY listing. With a 4.4 GB store that is the
// single largest allocator in the process (measured: 651 MB of io.ReadAll,
// 29% of all allocation) and it repeats on every scroll-paginate, ctrl+h, and
// chat exit.
//
// The extracted metadata is tiny (~1 KB) and a conversation file only changes
// when it is written, so we cache the meta view keyed by conversation ID and
// validate it against the file's (modTime, size). A stale or rewritten file is
// detected and re-read automatically; an unchanged file costs one stat.
type metaCacheEntry struct {
	modTime time.Time
	size    int64
	preview int // FirstMessagePreview the entry was built with
	conv    *conversation.Conversation
}

// lookupMeta returns a cached metadata conversation when the on-disk file is
// unchanged and was extracted with the same preview depth.
func (d *DirectoryFileStorage) lookupMeta(id string, fi os.FileInfo, preview int) *conversation.Conversation {
	if fi == nil {
		return nil
	}
	d.metaMu.RLock()
	e, ok := d.metaCache[id]
	d.metaMu.RUnlock()
	if !ok || e.preview != preview || e.size != fi.Size() || !e.modTime.Equal(fi.ModTime()) {
		return nil
	}
	return cloneConversation(e.conv)
}

// storeMeta records the metadata view of a file for later reuse.
func (d *DirectoryFileStorage) storeMeta(id string, fi os.FileInfo, preview int, conv *conversation.Conversation) {
	if fi == nil || conv == nil {
		return
	}
	d.metaMu.Lock()
	if d.metaCache == nil {
		d.metaCache = make(map[string]*metaCacheEntry)
	}
	d.metaCache[id] = &metaCacheEntry{
		modTime: fi.ModTime(),
		size:    fi.Size(),
		preview: preview,
		conv:    cloneConversation(conv),
	}
	d.metaMu.Unlock()
}

// ── pooledListFromDirs ────────────────────────────────────────────────────────

// pooledListFromDirs loads conversations using parallel I/O and pooled buffers.
// When workspacePath is set it scans exactly one directory.
// Caller must NOT hold d.mu.
func (d *DirectoryFileStorage) pooledListFromDirs(workspacePath string, filter Filter) ([]*conversation.Conversation, error) {
	var dirs []string
	if workspacePath != "" {
		dirs = []string{EncodeWorkspacePath(workspacePath)}
	} else {
		d.mu.RLock()
		entries, err := os.ReadDir(d.baseDir)
		d.mu.RUnlock()
		if err != nil {
			return nil, fmt.Errorf("read base dir: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() && IsWorkspaceDir(e.Name()) {
				dirs = append(dirs, e.Name())
			}
		}
	}
	if len(dirs) == 0 {
		return nil, nil
	}

	type fileJob struct {
		id, path          string
		workspacePathHint string
		fi                os.FileInfo
	}
	var jobs []fileJob
	var direct []*conversation.Conversation

	// Computed up front: the metadata cache below is keyed on preview depth,
	// and the entry loop needs to know whether this is a meta query.
	isMeta := filter.ExcludeMessages || filter.FirstMessagePreview > 0
	preview := filter.FirstMessagePreview
	if filter.ExcludeMessages {
		preview = 0
	}

	for _, wsDir := range dirs {
		wsPath := filepath.Join(d.baseDir, wsDir)
		workspacePathHint, _ := DecodeWorkspacePath(wsDir)
		entries, err := os.ReadDir(wsPath)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
				continue
			}
			id := strings.TrimSuffix(e.Name(), ".json")

			d.mu.RLock()
			cached, ok := d.cache[id]
			d.mu.RUnlock()
			if ok {
				c := cloneConversation(cached.conv)
				normalizeWorkspacePath(c, workspacePathHint)
				applyMessageFilter(c, filter)
				direct = append(direct, c)
				continue
			}

			// Metadata queries: reuse the cached meta view when the file is
			// byte-for-byte unchanged (same modTime + size). This turns a
			// full re-read of the workspace into one stat per file.
			var fi os.FileInfo
			if isMeta {
				if info, statErr := e.Info(); statErr == nil {
					fi = info
					if c := d.lookupMeta(id, fi, preview); c != nil {
						direct = append(direct, c)
						continue
					}
				}
			}

			jobs = append(jobs, fileJob{
				id:                id,
				path:              filepath.Join(wsPath, e.Name()),
				workspacePathHint: workspacePathHint,
				fi:                fi,
			})
		}
	}

	if len(jobs) == 0 {
		return direct, nil
	}

	resultCh := make(chan loadResult, len(jobs))

	pool := getIOPool() // singleton, no allocation

	var wg sync.WaitGroup
	wg.Add(len(jobs))
	for _, job := range jobs {
		j := job
		p := preview
		m := isMeta
		f := filter
		pool.Submit(func() {
			defer wg.Done()
			conv, err := loadFile(j.path, f, m, p, j.workspacePathHint)
			if err == nil && m && conv != nil {
				d.storeMeta(j.id, j.fi, p, conv)
			}
			resultCh <- loadResult{conv: conv, err: err}
		})
	}
	go func() { wg.Wait(); close(resultCh) }()

	results := make([]*conversation.Conversation, 0, len(jobs)+len(direct))
	results = append(results, direct...)
	for r := range resultCh {
		if r.err == nil && r.conv != nil {
			results = append(results, r.conv)
		}
	}
	return results, nil
}

// loadFile reads one conversation file. Metadata queries avoid decoding the
// messages array into conversation structs, while both modes read the complete
// file so fields are independent of JSON field order.
func loadFile(path string, filter Filter, isMeta bool, preview int, workspacePathHint string) (*conversation.Conversation, error) {
	if isMeta {
		conv, err := loadFileMeta(path, preview)
		if err == nil {
			normalizeWorkspacePath(conv, workspacePathHint)
		}
		return conv, err
	}

	// Full load — read entire file into pooled buffer
	buf := getBuf()
	defer putBuf(buf)
	data, err := readFileIntoBuf(path, buf)
	if err != nil {
		return nil, err
	}
	pd := decoderPool.Get().(*pooledDecoder)
	pd.reset(data)
	var conv conversation.Conversation
	decErr := pd.dec.Decode(&conv)
	decoderPool.Put(pd)
	if decErr != nil {
		return nil, decErr
	}
	normalizeWorkspacePath(&conv, workspacePathHint)
	applyMessageFilter(&conv, filter)
	return &conv, nil
}

// loadFileMeta reads the complete JSON document before extracting metadata.
// Legacy files may place summary and metadata after a multi-megabyte messages
// array; parsing a prefix can appear successful while silently omitting those
// trailing fields. Correctness therefore takes precedence over the old bounded
// prefix optimization.
//
// The read goes through the shared buffer pool rather than io.ReadAll: this
// file's own header promises "sync.Pool of reusable read buffers — eliminates
// per-file heap allocs", but the meta path bypassed it and became the largest
// allocator in the process. extractMeta copies every field it keeps
// (string(value), an explicit make+copy for the preview message, and
// json.Unmarshal for metadata.custom), so it retains no subslice of the
// buffer and reuse is safe.
func loadFileMeta(path string, preview int) (*conversation.Conversation, error) {
	buf := getBuf()
	defer putBuf(buf)

	full, err := readFileIntoBuf(path, buf)
	if err != nil {
		return nil, err
	}
	return extractMeta(full, preview)
}

// readFileIntoBuf reads path into a reused buffer slice.
func readFileIntoBuf(path string, buf *[]byte) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := int(info.Size())
	if size > cap(*buf) {
		// File is bigger than the pooled buffer — allocate exact size
		// (don't grow the pool buffer above its cap)
		data := make([]byte, size)
		n, readErr := io.ReadFull(f, data)
		if readErr != nil {
			if _, seekErr := f.Seek(0, 0); seekErr != nil {
				return nil, readErr
			}
			return io.ReadAll(f)
		}
		return data[:n], nil
	}
	*buf = (*buf)[:size]
	n, readErr := io.ReadFull(f, *buf)
	if readErr != nil {
		if _, seekErr := f.Seek(0, 0); seekErr != nil {
			return nil, readErr
		}
		return io.ReadAll(f)
	}
	return (*buf)[:n], nil
}
