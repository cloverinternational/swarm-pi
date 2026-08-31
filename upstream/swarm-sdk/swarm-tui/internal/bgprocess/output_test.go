package bgprocess

import (
	"strings"
	"testing"
	"time"
)

func TestMemoryBuffer_BasicWrite(t *testing.T) {
	buf := NewMemoryBuffer(1024)
	defer buf.Close()

	err := buf.WriteLine("stdout", "test line")
	if err != nil {
		t.Fatalf("WriteLine() error = %v", err)
	}

	lines, err := buf.Lines(LineQueryOpts{})
	if err != nil {
		t.Fatalf("Lines() error = %v", err)
	}

	if len(lines) != 1 {
		t.Errorf("Expected 1 line, got %d", len(lines))
	}

	if lines[0].Content != "test line" {
		t.Errorf("Content = %v, want 'test line'", lines[0].Content)
	}

	if lines[0].Stream != "stdout" {
		t.Errorf("Stream = %v, want 'stdout'", lines[0].Stream)
	}
}

func TestMemoryBuffer_MultipleLines(t *testing.T) {
	buf := NewMemoryBuffer(1024)
	defer buf.Close()

	lines := []string{"line1", "line2", "line3"}
	for i, line := range lines {
		stream := "stdout"
		if i == 2 {
			stream = "stderr"
		}
		err := buf.WriteLine(stream, line)
		if err != nil {
			t.Fatalf("WriteLine() error = %v", err)
		}
	}

	allLines, err := buf.Lines(LineQueryOpts{})
	if err != nil {
		t.Fatalf("Lines() error = %v", err)
	}

	if len(allLines) != 3 {
		t.Errorf("Expected 3 lines, got %d", len(allLines))
	}

	// Test stdout filter
	stdoutLines, err := buf.Lines(LineQueryOpts{Stream: "stdout"})
	if err != nil {
		t.Fatalf("Lines(stdout) error = %v", err)
	}

	if len(stdoutLines) != 2 {
		t.Errorf("Expected 2 stdout lines, got %d", len(stdoutLines))
	}

	// Test stderr filter
	stderrLines, err := buf.Lines(LineQueryOpts{Stream: "stderr"})
	if err != nil {
		t.Fatalf("Lines(stderr) error = %v", err)
	}

	if len(stderrLines) != 1 {
		t.Errorf("Expected 1 stderr line, got %d", len(stderrLines))
	}
}

func TestMemoryBuffer_MaxSize(t *testing.T) {
	// Small buffer that can hold ~2 lines
	buf := NewMemoryBuffer(20)
	defer buf.Close()

	// Write 5 lines
	for range 5 {
		err := buf.WriteLine("stdout", "test line")
		if err != nil {
			t.Fatalf("WriteLine() error = %v", err)
		}
	}

	// Should have evicted old lines
	lines, err := buf.Lines(LineQueryOpts{})
	if err != nil {
		t.Fatalf("Lines() error = %v", err)
	}

	// Should have fewer than 5 lines due to size limit
	if len(lines) >= 5 {
		t.Errorf("Expected < 5 lines due to size limit, got %d", len(lines))
	}
}

func TestMemoryBuffer_Tail(t *testing.T) {
	buf := NewMemoryBuffer(1024)
	defer buf.Close()

	// Write 10 lines
	for range 10 {
		buf.WriteLine("stdout", "line")
	}

	// Get last 3
	tail, err := buf.Tail(3)
	if err != nil {
		t.Fatalf("Tail() error = %v", err)
	}

	if len(tail) != 3 {
		t.Errorf("Expected 3 lines, got %d", len(tail))
	}

	// Line numbers should be 8, 9, 10
	if tail[0].LineNumber != 8 {
		t.Errorf("First tail line number = %d, want 8", tail[0].LineNumber)
	}
}

func TestMemoryBuffer_Since(t *testing.T) {
	buf := NewMemoryBuffer(1024)
	defer buf.Close()

	// Write some lines
	buf.WriteLine("stdout", "old line")
	time.Sleep(10 * time.Millisecond)

	checkpoint := time.Now()
	time.Sleep(10 * time.Millisecond)

	buf.WriteLine("stdout", "new line 1")
	buf.WriteLine("stdout", "new line 2")

	// Get lines since checkpoint
	lines, err := buf.Since(checkpoint)
	if err != nil {
		t.Fatalf("Since() error = %v", err)
	}

	if len(lines) != 2 {
		t.Errorf("Expected 2 lines since checkpoint, got %d", len(lines))
	}
}

func TestMemoryBuffer_PatternFilter(t *testing.T) {
	buf := NewMemoryBuffer(1024)
	defer buf.Close()

	buf.WriteLine("stdout", "error: something failed")
	buf.WriteLine("stdout", "info: all good")
	buf.WriteLine("stdout", "error: another failure")

	// Filter by pattern
	lines, err := buf.Lines(LineQueryOpts{
		Pattern: "error:",
	})
	if err != nil {
		t.Fatalf("Lines() with pattern error = %v", err)
	}

	if len(lines) != 2 {
		t.Errorf("Expected 2 error lines, got %d", len(lines))
	}
}

func TestMemoryBuffer_MaxLinesLimit(t *testing.T) {
	buf := NewMemoryBuffer(1024)
	defer buf.Close()

	// Write 10 lines
	for range 10 {
		buf.WriteLine("stdout", "line")
	}

	// Get with limit
	lines, err := buf.Lines(LineQueryOpts{MaxLines: 5})
	if err != nil {
		t.Fatalf("Lines() error = %v", err)
	}

	if len(lines) != 5 {
		t.Errorf("Expected 5 lines (limit), got %d", len(lines))
	}
}

func TestMemoryBuffer_FromLine(t *testing.T) {
	buf := NewMemoryBuffer(1024)
	defer buf.Close()

	// Write 10 lines
	for range 10 {
		buf.WriteLine("stdout", "line")
	}

	// Get from line 5
	lines, err := buf.Lines(LineQueryOpts{FromLine: 5})
	if err != nil {
		t.Fatalf("Lines() error = %v", err)
	}

	if len(lines) != 6 { // Lines 5-10
		t.Errorf("Expected 6 lines, got %d", len(lines))
	}

	if lines[0].LineNumber != 5 {
		t.Errorf("First line number = %d, want 5", lines[0].LineNumber)
	}
}

func TestMemoryBuffer_Stream(t *testing.T) {
	buf := NewMemoryBuffer(1024)
	defer buf.Close()

	ctx := t.Context()

	ch, err := buf.Stream(ctx)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	// Write lines in background
	go func() {
		time.Sleep(10 * time.Millisecond)
		buf.WriteLine("stdout", "line1")
		time.Sleep(10 * time.Millisecond)
		buf.WriteLine("stdout", "line2")
	}()

	// Read from stream
	var received []string
	timeout := time.After(200 * time.Millisecond)

	for len(received) < 2 {
		select {
		case line := <-ch:
			received = append(received, line.Content)
		case <-timeout:
			t.Fatal("Timeout waiting for stream lines")
		}
	}

	if len(received) != 2 {
		t.Errorf("Expected 2 streamed lines, got %d", len(received))
	}
}

func TestMemoryBuffer_Size(t *testing.T) {
	buf := NewMemoryBuffer(1024)
	defer buf.Close()

	initialSize := buf.Size()
	if initialSize != 0 {
		t.Errorf("Initial size = %d, want 0", initialSize)
	}

	buf.WriteLine("stdout", "test")
	size := buf.Size()
	if size <= 0 {
		t.Errorf("Size after write = %d, want > 0", size)
	}
}

func TestMemoryBuffer_LineCount(t *testing.T) {
	buf := NewMemoryBuffer(1024)
	defer buf.Close()

	if buf.LineCount() != 0 {
		t.Errorf("Initial line count = %d, want 0", buf.LineCount())
	}

	for range 5 {
		buf.WriteLine("stdout", "line")
	}

	if buf.LineCount() != 5 {
		t.Errorf("Line count = %d, want 5", buf.LineCount())
	}
}

func TestMemoryBuffer_WriteAfterClose(t *testing.T) {
	buf := NewMemoryBuffer(1024)
	buf.Close()

	err := buf.WriteLine("stdout", "test")
	if err != ErrBufferClosed {
		t.Errorf("WriteLine() after close error = %v, want ErrBufferClosed", err)
	}
}

func TestMemoryBuffer_IoWriter(t *testing.T) {
	buf := NewMemoryBuffer(1024)
	defer buf.Close()

	// Test io.Writer interface
	data := []byte("line1\nline2\nline3\n")
	n, err := buf.Write(data)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if n != len(data) {
		t.Errorf("Write() returned %d, want %d", n, len(data))
	}

	// Verify lines were captured
	lines, err := buf.Lines(LineQueryOpts{})
	if err != nil {
		t.Fatalf("Lines() error = %v", err)
	}

	if len(lines) < 3 {
		t.Errorf("Expected at least 3 lines, got %d", len(lines))
	}
}

func TestFileBuffer_BasicWrite(t *testing.T) {
	tmpFile := "/tmp/bgprocess_test_" + time.Now().Format("20060102150405") + ".log"
	defer func() {
		// Cleanup
		// os.Remove(tmpFile)
	}()

	buf, err := NewFileBuffer(tmpFile, 1024*1024)
	if err != nil {
		t.Fatalf("NewFileBuffer() error = %v", err)
	}
	defer buf.Close()

	err = buf.WriteLine("stdout", "test line")
	if err != nil {
		t.Fatalf("WriteLine() error = %v", err)
	}

	lines, err := buf.Lines(LineQueryOpts{})
	if err != nil {
		t.Fatalf("Lines() error = %v", err)
	}

	if len(lines) != 1 {
		t.Errorf("Expected 1 line, got %d", len(lines))
	}

	if lines[0].Content != "test line" {
		t.Errorf("Content = %v, want 'test line'", lines[0].Content)
	}
}

func TestFileBuffer_Path(t *testing.T) {
	tmpFile := "/tmp/bgprocess_test_path.log"
	defer func() {
		// os.Remove(tmpFile)
	}()

	buf, err := NewFileBuffer(tmpFile, 1024*1024)
	if err != nil {
		t.Fatalf("NewFileBuffer() error = %v", err)
	}
	defer buf.Close()

	if buf.Path() != tmpFile {
		t.Errorf("Path() = %v, want %v", buf.Path(), tmpFile)
	}
}

func TestNewOutputBuffer_Memory(t *testing.T) {
	buf, err := NewOutputBuffer(OutputBufferConfig{
		Type:    BufferMemory,
		MaxSize: 1024,
	})
	if err != nil {
		t.Fatalf("NewOutputBuffer() error = %v", err)
	}
	defer buf.Close()

	if _, ok := buf.(*MemoryBuffer); !ok {
		t.Error("Expected *MemoryBuffer type")
	}
}

func TestNewOutputBuffer_File(t *testing.T) {
	tmpFile := "/tmp/bgprocess_test_output.log"
	defer func() {
		// os.Remove(tmpFile)
	}()

	buf, err := NewOutputBuffer(OutputBufferConfig{
		Type:     BufferFile,
		FilePath: tmpFile,
		MaxSize:  1024,
	})
	if err != nil {
		t.Fatalf("NewOutputBuffer() error = %v", err)
	}
	defer buf.Close()

	if _, ok := buf.(*FileBuffer); !ok {
		t.Error("Expected *FileBuffer type")
	}
}

func TestNewOutputBuffer_InvalidFileConfig(t *testing.T) {
	_, err := NewOutputBuffer(OutputBufferConfig{
		Type: BufferFile,
		// Missing FilePath
	})
	if err == nil {
		t.Error("Expected error for file buffer without path")
	}

	if !strings.Contains(err.Error(), "file_path") {
		t.Errorf("Error should mention file_path, got: %v", err)
	}
}

func TestMultiWriter_Stdout(t *testing.T) {
	buf := NewMemoryBuffer(1024)
	defer buf.Close()

	writer := NewStdoutWriter(buf)
	data := []byte("test line\n")

	n, err := writer.Write(data)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if n != len(data) {
		t.Errorf("Write() returned %d, want %d", n, len(data))
	}

	lines, _ := buf.Lines(LineQueryOpts{})
	if len(lines) != 1 {
		t.Fatalf("Expected 1 line, got %d", len(lines))
	}

	if lines[0].Stream != "stdout" {
		t.Errorf("Stream = %v, want stdout", lines[0].Stream)
	}
}

func TestMultiWriter_Stderr(t *testing.T) {
	buf := NewMemoryBuffer(1024)
	defer buf.Close()

	writer := NewStderrWriter(buf)
	data := []byte("error line\n")

	n, err := writer.Write(data)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if n != len(data) {
		t.Errorf("Write() returned %d, want %d", n, len(data))
	}

	lines, _ := buf.Lines(LineQueryOpts{})
	if len(lines) != 1 {
		t.Fatalf("Expected 1 line, got %d", len(lines))
	}

	if lines[0].Stream != "stderr" {
		t.Errorf("Stream = %v, want stderr", lines[0].Stream)
	}
}
