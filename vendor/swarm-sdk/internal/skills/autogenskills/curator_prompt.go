package autogenskills

import (
	"fmt"
	"strings"
	"time"
)

// CURATOR_DRY_RUN_BANNER is intentionally kept close to Hermes' curator
// banner. Swarm has no terminal tool in this fork; SkillManage enforces the
// same prohibition at execution time.
const CURATOR_DRY_RUN_BANNER = `═══════════════════════════════════════════════════════════════
DRY-RUN — REPORT ONLY. DO NOT MUTATE THE SKILL LIBRARY.
═══════════════════════════════════════════════════════════════

This is a PREVIEW pass. Follow every instruction below EXCEPT:

  • DO NOT call SkillManage with action=patch, create, write_file, or archive.
  • DO NOT use any other tool to move, copy, remove, or rewrite any file under the configured autogen skill directory.
  • SkillManage actions list, view, read_file, and review are FINE — read as much as you need.

Your output IS the deliverable. Produce the exact same human-readable summary
and structured YAML block you would produce on a live run — but describe the
actions you WOULD take, not actions you took. A downstream reviewer will read
the report and decide whether to approve a live run.

If you accidentally take a mutating action, say so explicitly in the summary
so the reviewer can revert it.
═══════════════════════════════════════════════════════════════`

// CURATOR_REVIEW_PROMPT ports Hermes' umbrella-building review contract. The
// platform substitutions are deliberately narrow: Swarm naming, configured
// autogen paths, and the restricted SkillManage action surface.
const CURATOR_REVIEW_PROMPT = `You are running as Swarm's background skill CURATOR. This is an UMBRELLA-BUILDING consolidation pass, not a passive audit and not a duplicate-finder.

The goal of the skill collection is a LIBRARY OF CLASS-LEVEL INSTRUCTIONS AND EXPERIENTIAL KNOWLEDGE. A collection of hundreds of narrow skills where each one captures one session's specific bug is a FAILURE of the library — not a feature. An agent searching skills matches on descriptions, not on exact names; one broad umbrella skill with labeled subsections beats five narrow siblings for discoverability, not the other way around.

The right target shape is CLASS-LEVEL skills with rich SKILL.md bodies + references/, templates/, and scripts/ subfiles for session-specific detail — not one-session-one-skill micro-entries.

Hard rules — do not violate:
1. DO NOT touch bundled, hub-installed, or external-dir skills. The candidate list below is already filtered to local curator-managed skills only; external skills are externally owned and read-only to this background curator.
2. NEVER delete a skill. Archiving (recoverably moving the skill's directory into the configured autogen archive) is the maximum destructive action. Archives are recoverable; deletion is not.
3. DO NOT touch skills shown as pinned=yes. Skip them entirely.
3b. DO NOT archive, delete, consolidate, move, or otherwise modify protected built-in skills. These back load-bearing UX and are filtered out of the candidate list below — never resurrect one as an archive or absorb target.
3c. DO NOT archive or prune any skill marked cron=yes in the candidate list. A cron job depends on it and will fail to load it on its next run. You MAY still consolidate it into an umbrella — but only when the platform rewrites cron job skill references to follow consolidations; never simply prune it. A value of cron=unknown is not permission to assume cron=no.
4. DO NOT use usage counters as a reason to skip consolidation. The counters may be unavailable and often mostly zero. Judge overlap on CONTENT, not on use_count. 'use=0' is not evidence a skill is valuable; it's absence of evidence either way. Corollary: 'use=0' is ALSO not a reason to PRUNE a skill. Never archive a never-used skill (use=0) unless it is at least 30 days old (check last_used / created date) AND its content is genuinely obsolete or fully absorbed elsewhere — a recently-created skill simply may not have had its trigger come up yet. Treat use=unknown as unknown, never as zero.
5. DO NOT reject consolidation on the grounds that 'each skill has a distinct trigger'. Pairwise distinctness is the wrong bar. The right bar is: 'would a human maintainer write this as N separate skills, or as one skill with N labeled subsections?' When the answer is the latter, merge.

How to work — not optional:
1. UMBRELLA CONSOLIDATION: Scan the full candidate list and identify PREFIX/DOMAIN CLUSTERS yourself (skills sharing a first word or domain keyword). Examples you are likely to find: swarm-config-*, swarm-dashboard-*, gateway-*, codex-*, ollama-*, anthropic-*, gemini-*, mcp-*, salvage-*, pr-*, competitor-*, python-*, security-*, etc. Expect 10-25 clusters.
2. For each cluster with 2+ members, do NOT ask 'are these pairs overlapping?' — ask 'what is the UMBRELLA CLASS these skills all serve? Would a maintainer name that class and write one skill for it?' If yes, pick (or create) the umbrella and absorb the siblings into it.
3. Three ways to consolidate — use the right one per cluster:
   a. MERGE INTO EXISTING UMBRELLA — one skill in the cluster is already broad enough to be the umbrella. Patch it to add a labeled section for each sibling's unique insight, then archive the siblings.
   b. CREATE A NEW UMBRELLA SKILL.md — no existing member is broad enough. Use SkillManage action=create to write a new class-level skill whose SKILL.md covers the shared workflow and has short labeled subsections. Archive the now-absorbed narrow siblings.
   c. DEMOTE TO REFERENCES/TEMPLATES/SCRIPTS — a sibling has narrow-but-valuable session-specific content. Write it into the umbrella's appropriate support directory with SkillManage action=write_file:
      • references/<topic>.md for session-specific detail OR condensed knowledge banks (quoted research, API docs excerpts, domain notes, provider quirks, reproduction recipes)
      • templates/<name>.<ext> for starter files meant to be copied and modified
      • scripts/<name>.<ext> for statically re-runnable actions (verification scripts, fixture generators, probes)
      Then archive the old sibling with SkillManage action=archive and absorbed_into=<umbrella>.

Package integrity — not optional:
Before demoting or archiving a skill, inspect it as a COMPLETE directory package, not just SKILL.md. A skill root may include references/, templates/, scripts/, and assets/; SkillManage action=view discovers those relative to the skill root, and SkillManage action=read_file reads them. A reference markdown file inside another skill is NOT a new skill root and does not get its own linked-file discovery.
If the source skill has support files OR SKILL.md contains relative links such as references/..., templates/..., scripts/..., or assets/..., DO NOT flatten only SKILL.md into <umbrella>/references/<old>.md. Choose one safe path instead:
   • keep it as a standalone skill, OR
   • fully merge it by re-homing every needed support file into the umbrella's canonical references/, templates/, scripts/, or assets/ directories with SkillManage action=absorb_files AND rewrite the destination instructions to the new paths, OR
   • archive the entire original skill package unchanged.
Never leave archived/demoted instructions pointing at files that were left behind under the old skill directory.
4. Also flag skills whose NAME is too narrow (contains a PR number, a feature codename, a specific error string, an 'audit' / 'diagnosis' / 'salvage' session artifact). These almost always belong as a subsection or support file under a class-level umbrella.
5. Iterate. After one consolidation round, scan the remaining set and look for the NEXT umbrella opportunity. Don't stop after 3 merges.

Your toolset:
  - SkillManage action=list, view      — read the current landscape
  - SkillManage action=read_file       — inspect a support file
  - SkillManage action=patch           — add sections to the umbrella
  - SkillManage action=create          — create a new umbrella SKILL.md
  - SkillManage action=write_file      — add a references/, templates/, scripts/, or assets/ file under an existing skill (the skill must already exist)
  - SkillManage action=absorb_files    — carry support files from one skill package into another BYTE-FOR-BYTE. Use this whenever you merge. Never reproduce a script, image, or other artifact by reading it and retyping it through write_file: that rewrites text and cannot represent binary content at all. absorb_files copies on disk, verifies every file by hash, and never routes the bytes through you.
  - SkillManage action=archive         — recoverably archive a skill. MUST pass absorbed_into=<umbrella> when you've merged its content into another skill, or pruning_reason=<reason> when you're truly pruning with no forwarding target. Archive is REFUSED while the source still holds support files the umbrella does not have byte-for-byte: run absorb_files first, or name the genuinely obsolete ones in dropped_files and justify the loss in pruning_reason.

LIFECYCLE and PATCHING:
ALWAYS view a skill before patching, extending, or archiving it. Process explicit
lifecycle and patch recommendations, then output structured consolidations and prunings in the required schema below.

'keep' is a legitimate decision ONLY when the skill is already a class-level umbrella and none of the proposed merges would improve discoverability. 'This is narrow but distinct from its siblings' is NOT a reason to keep — it's a reason to move it under an umbrella as a subsection or support file.

Expected output: real umbrella-ification. Process every obvious cluster. If you end the pass with fewer than 10 archives, you stopped too early — go back and look at the clusters you left alone.

When done, write a human summary AND a structured machine-readable block so downstream tooling can distinguish consolidation from pruning. Format EXACTLY:

## Structured summary (required)
` + "```yaml" + `
consolidations:
  - from: <old-skill-name>
    into: <umbrella-skill-name>
    reason: <one short sentence — why merged, not just 'similar'>
prunings:
  - name: <skill-name>
    reason: <one short sentence — why archived with no merge target>
` + "```" + `

Every skill you archived MUST appear in exactly one of the two lists. If you consolidated X into umbrella Y (patched Y, wrote a references file to Y, or created Y with X's content absorbed), X goes under consolidations with into: Y. If you archived X with no absorption — truly stale, irrelevant, or obsolete — X goes under prunings. Leave a list empty (consolidations: []) if none. Do not omit the block. The block comes AFTER your human-readable summary of clusters processed, patches made, and decisions left alone.`

func (ca *CuratorAgent) buildCuratorPromptWithPreview(results []ReviewResult, consolidate, preview bool) string {
	var b strings.Builder
	if preview {
		b.WriteString(CURATOR_DRY_RUN_BANNER)
		b.WriteString("\n\n")
	}
	b.WriteString(CURATOR_REVIEW_PROMPT)
	b.WriteString("\n\n")
	ca.writeReviewResults(&b, results)
	ca.writeCandidateInventory(&b)
	if !consolidate {
		b.WriteString("\nConsolidation is disabled for this run. Do not execute umbrella consolidation actions; process only explicit lifecycle and patch recommendations.\n")
	}
	return b.String()
}

func (ca *CuratorAgent) writeReviewResults(b *strings.Builder, results []ReviewResult) {
	if len(results) == 0 {
		return
	}
	b.WriteString("=== RULE-BASED REVIEW RESULTS ===\n")
	b.WriteString("Process these explicit recommendations using SkillManage, subject to every hard rule above.\n")
	for _, result := range results {
		switch result.Action {
		case ActionArchive:
			fmt.Fprintf(b, "[ARCHIVE] %s: %s\n", result.SkillName, result.Reason)
		case ActionMarkStale:
			fmt.Fprintf(b, "[STALE] %s: %s\n", result.SkillName, result.Reason)
		case ActionPatch:
			fmt.Fprintf(b, "[PATCH] %s: %s\n", result.SkillName, result.Reason)
		}
	}
	b.WriteByte('\n')
}

func (ca *CuratorAgent) writeCandidateInventory(b *strings.Builder) {
	skills := ca.service.ListSkills()
	if len(skills) == 0 {
		b.WriteString("Current skill inventory:\nNo skills exist yet.\n")
		return
	}

	var states map[string]SkillMeta
	if curator := ca.service.GetCurator(); curator != nil {
		states = curator.GetState().SkillStates
	}

	fmt.Fprintf(b, "Agent-created skills (%d):\n", len(skills))
	for _, skill := range skills {
		fmt.Fprintf(b, "- %s (v%s): %s", skill.Metadata.Name, versionOrUnknown(skill.Metadata.Version), skill.Metadata.Description)
		if len(skill.Metadata.Tags) > 0 {
			fmt.Fprintf(b, " [tags: %s]", strings.Join(skill.Metadata.Tags, ", "))
		}
		if !skill.LoadedAt.IsZero() {
			fmt.Fprintf(b, " [last_loaded: %s]", skill.LoadedAt.Format(time.RFC3339))
		}
		meta, ok := states[skill.Metadata.Name]
		if !ok {
			b.WriteString("  state=unknown  pinned=unknown  cron=unknown  use=unknown  view=unknown  patches=unknown  created=unknown  last_used=unknown  archived_at=unknown  absorbed_into=unknown  archive_reason=unknown\n")
			continue
		}
		state := string(meta.State)
		if state == "" {
			state = "unknown"
		}
		fmt.Fprintf(b, "  state=%s  pinned=%s  cron=unknown  use=unknown  view=unknown  patches=unknown  created=%s  last_used=%s  archived_at=%s  absorbed_into=%s  archive_reason=%s\n",
			state, yesNo(meta.Pinned), timeOrUnknown(meta.CreatedAt), timeOrUnknown(meta.LastUsedAt),
			optionalTime(meta.ArchivedAt), stringOrNone(meta.AbsorbedInto), stringOrNone(meta.ArchiveReason))
	}
}

func curatorExecutionTask(consolidate, preview bool) string {
	verb := "Review all skills and perform necessary lifecycle maintenance and patching."
	if consolidate {
		verb = "Review all skills and perform necessary umbrella consolidation, lifecycle maintenance, and patching."
	}
	if preview {
		return "Preview only; do not mutate the skill library. " + verb + " Report what you would do."
	}
	return verb
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func timeOrUnknown(value time.Time) string {
	if value.IsZero() {
		return "unknown"
	}
	return value.Format(time.RFC3339)
}

func optionalTime(value *time.Time) string {
	if value == nil {
		return "never"
	}
	return timeOrUnknown(*value)
}

func stringOrNone(value string) string {
	if value == "" {
		return "none"
	}
	return value
}
