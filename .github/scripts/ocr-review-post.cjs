'use strict';

const fs = require('fs');

const SUMMARY_MARKER = '<!-- clover-ocr-review -->';
const INLINE_MARKER = '<!-- clover-ocr-inline -->';
const MAX_INLINE_COMMENTS = 40;

function readText(path, fallback = '') {
  try {
    return fs.readFileSync(path, 'utf8').trim();
  } catch (_) {
    return fallback;
  }
}

function parseJsonOutput(raw) {
  const trimmed = String(raw || '').trim();
  if (!trimmed) return null;
  try {
    return JSON.parse(trimmed);
  } catch (_) {
    const first = trimmed.indexOf('{');
    const last = trimmed.lastIndexOf('}');
    if (first === -1 || last <= first) return null;
    try {
      return JSON.parse(trimmed.slice(first, last + 1));
    } catch (_) {
      return null;
    }
  }
}

function extractStatuses(value, into = []) {
  if (Array.isArray(value)) {
    for (const item of value) extractStatuses(item, into);
  } else if (value && typeof value === 'object') {
    for (const [key, child] of Object.entries(value)) {
      if (key === 'status' && typeof child === 'string') {
        into.push(child);
      }
      extractStatuses(child, into);
    }
  }
  return into;
}

function completeCoverageIsConsistent(manifest) {
  const coverage = manifest && manifest.coverage;
  if (!coverage || typeof coverage !== 'object' || Array.isArray(coverage)) return false;
  const names = ['selected', 'completed', 'reused', 'failed', 'waived'];
  if (!names.every((name) => Array.isArray(coverage[name]))) return false;
  if (coverage.failed.length !== 0) return false;

  const selectedIds = coverage.selected.map((item) => item && item.item_id);
  const coveredIds = [
    ...coverage.completed,
    ...coverage.reused,
    ...coverage.waived,
  ].map((item) => item && item.item_id);
  if ([...selectedIds, ...coveredIds].some(
    (id) => typeof id !== 'string' || id.length === 0,
  )) {
    return false;
  }

  const selected = new Set(selectedIds);
  const covered = new Set(coveredIds);
  return selected.size === selectedIds.length
    && covered.size === coveredIds.length
    && covered.size === selected.size
    && [...covered].every((id) => selected.has(id));
}

function classifyCoverage(parsed, exitCode) {
  if (!Number.isInteger(exitCode) || exitCode !== 0) return 'failed';

  const hasManifest = parsed
    && typeof parsed === 'object'
    && !Array.isArray(parsed)
    && Object.prototype.hasOwnProperty.call(parsed, 'manifest');
  if (hasManifest) {
    const manifest = parsed.manifest;
    if (!manifest || typeof manifest !== 'object' || Array.isArray(manifest)) {
      return 'unknown';
    }
    // OCR's versioned run manifest is the authoritative coverage contract.
    // Do not guess at future schemas: an unknown version must remain unknown
    // until this publisher is updated to understand it.
    if (manifest.schema_version !== 'ocr.run-manifest/v1') return 'unknown';
    switch (manifest.terminal_state) {
      case 'complete':
        return completeCoverageIsConsistent(manifest)
          ? 'complete_best_effort'
          : 'unknown';
      case 'partial':
        return 'partial';
      case 'failed':
        return 'failed';
      default:
        return 'unknown';
    }
  }

  // Backward compatibility for pre-manifest OCR output.
  const statuses = extractStatuses(parsed);
  if (statuses.length === 0) return 'unknown';
  if (statuses.every((status) => status === 'success')) {
    return 'complete_best_effort';
  }
  if (statuses.some((status) => [
    'completed_with_warnings',
    'completed_with_errors',
    'budget_exceeded',
  ].includes(status))) {
    return 'partial';
  }
  return 'unknown';
}

function validRightLines(patch) {
  const valid = new Set();
  if (typeof patch !== 'string') return valid;
  let rightLine = 0;
  const lines = patch.split('\n');
  while (lines.at(-1) === '') lines.pop();
  for (const line of lines) {
    const hunk = /^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line);
    if (hunk) {
      rightLine = Number(hunk[1]);
      continue;
    }
    if (line.startsWith('+') && !line.startsWith('+++')) {
      valid.add(rightLine);
      rightLine += 1;
    } else if (line.startsWith('-') && !line.startsWith('---')) {
      // A removed line has no RIGHT-side line number.
    } else if (line.startsWith(' ')) {
      valid.add(rightLine);
      rightLine += 1;
    }
  }
  return valid;
}

function escapedDiagnostic(value, limit = 4000) {
  const replacements = {
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '`': '&#96;',
    '@': '@\u200b',
  };
  let escaped = '';
  for (const character of String(value || '')) {
    const replacement = replacements[character] || character;
    if (escaped.length + replacement.length > limit) break;
    escaped += replacement;
  }
  return escaped;
}

function sanitizedFindingText(value) {
  const sanitized = String(value || 'OCR finding')
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .trim();
  return escapedDiagnostic(sanitized, 6000);
}

function sanitizedPath(value) {
  return escapedDiagnostic(
    String(value || '').replace(/[\u0000-\u001f\u007f-\u009f]/g, ' '),
    1000,
  );
}

function findingText(finding) {
  const content = finding && (
    finding.content || finding.comment || finding.message || finding.description
  );
  const severity = finding && typeof finding.severity === 'string'
    ? finding.severity.trim()
    : '';
  const category = finding && typeof finding.category === 'string'
    ? finding.category.trim()
    : '';
  const label = [severity, category].filter(Boolean).join(' / ');
  return `${label ? `**${sanitizedFindingText(label)}**\n\n` : ''}${sanitizedFindingText(content)}`;
}

function normalizedFinding(finding) {
  const path = finding && typeof finding.path === 'string'
    ? finding.path.trim()
    : '';
  let start = Number(finding && (finding.start_line || finding.line));
  let end = Number(finding && (finding.end_line || finding.line));
  if (!Number.isInteger(start) || start <= 0) start = 0;
  if (!Number.isInteger(end) || end <= 0) end = start;
  if (start > 0 && end > 0 && start > end) [start, end] = [end, start];
  return { path, start, end, text: findingText(finding) };
}

function inlineKey({ path, line, body }) {
  return `${path}\u0000${line}\u0000${String(body || '').replace(INLINE_MARKER, '').trim()}`;
}

function isWorkflowBot(comment) {
  return Boolean(comment && comment.user)
    && comment.user.type === 'Bot'
    && ['github-actions[bot]', 'github-actions'].includes(comment.user.login);
}

async function postReview({ github, context, core }) {
  const owner = context.repo.owner;
  const repo = context.repo.repo;
  const pullNumber = Number(process.env.PR_NUMBER);
  const expectedHead = String(process.env.HEAD_SHA || '');
  const baseSha = String(process.env.BASE_SHA || '');
  const runUrl = `${context.serverUrl}/${owner}/${repo}/actions/runs/${context.runId}`;
  const exitRaw = readText('ocr-exit-code.txt', '1');
  const exitCode = Number.isInteger(Number(exitRaw)) ? Number(exitRaw) : 1;
  const parsed = parseJsonOutput(readText('ocr-safe-result.json'));
  const coverage = classifyCoverage(parsed, exitCode);
  const findings = parsed && Array.isArray(parsed.comments) ? parsed.comments : [];
  const diagnostic = escapedDiagnostic(
    readText('ocr-safe-stderr.log', '').split('\n').slice(0, 20).join('\n'),
  );

  if (!Number.isInteger(pullNumber) || pullNumber <= 0) {
    throw new Error('A valid PR_NUMBER is required to publish OCR output.');
  }

  const pull = await github.rest.pulls.get({
    owner, repo, pull_number: pullNumber,
  });
  const currentHead = pull.data.head.sha;
  const stale = currentHead !== expectedHead;
  const files = await github.paginate(github.rest.pulls.listFiles, {
    owner, repo, pull_number: pullNumber, per_page: 100,
  });
  const rightLinesByPath = new Map(files.map((file) => [
    file.filename, validRightLines(file.patch),
  ]));

  const existing = await github.paginate(github.rest.pulls.listReviewComments, {
    owner, repo, pull_number: pullNumber, per_page: 100,
  });
  const existingKeys = new Set(existing
    .filter((comment) => comment.commit_id === expectedHead
      && typeof comment.body === 'string'
      && comment.body.includes(INLINE_MARKER))
    .map((comment) => inlineKey({
      path: comment.path,
      line: comment.line || comment.original_line,
      body: comment.body,
    })));

  const inline = [];
  const unmapped = [];
  const seen = new Set();
  let alreadyPosted = 0;
  for (const rawFinding of findings) {
    const finding = normalizedFinding(rawFinding);
    const identity = `${finding.path}\u0000${finding.start}\u0000${finding.end}\u0000${finding.text}`;
    if (seen.has(identity)) continue;
    seen.add(identity);

    const validLines = rightLinesByPath.get(finding.path);
    let line = 0;
    if (validLines && finding.end > 0) {
      for (let candidate = finding.end; candidate >= Math.max(1, finding.start); candidate -= 1) {
        if (validLines.has(candidate)) {
          line = candidate;
          break;
        }
      }
    }
    const body = `${INLINE_MARKER}\n${finding.text}`;
    const key = inlineKey({ path: finding.path, line, body });
    if (line > 0 && existingKeys.has(key)) {
      alreadyPosted += 1;
    } else if (!stale && line > 0 && inline.length < MAX_INLINE_COMMENTS) {
      inline.push({ path: finding.path, line, side: 'RIGHT', body });
      existingKeys.add(key);
    } else {
      unmapped.push(finding);
    }
  }

  let inlineFailure = '';
  let postedInline = 0;
  if (inline.length > 0) {
    try {
      await github.rest.pulls.createReview({
        owner,
        repo,
        pull_number: pullNumber,
        commit_id: expectedHead,
        event: 'COMMENT',
        body: `${INLINE_MARKER}\nOpenCodeReview findings for \`${expectedHead.slice(0, 12)}\`.`,
        comments: inline,
      });
      postedInline = inline.length;
    } catch (error) {
      inlineFailure = String(error && error.message ? error.message : error).slice(0, 500);
      core.warning(`Could not post OCR inline review: ${inlineFailure}`);
      unmapped.push(...inline.map((comment) => ({
        path: comment.path,
        start: comment.line,
        end: comment.line,
        text: comment.body.replace(INLINE_MARKER, '').trim(),
      })));
    }
  }

  const body = [
    SUMMARY_MARKER,
    '## OpenCodeReview',
    '',
    `**Coverage:** \`${coverage}\``,
    `**Reviewed range:** \`${baseSha.slice(0, 12)}..${expectedHead.slice(0, 12)}\``,
    `**Findings:** ${findings.length} total; ${postedInline} new inline; ${alreadyPosted} already posted; ${unmapped.length} summarized`,
    `**Model route:** Plexus \`swarm-main\``,
    '',
    '> Advisory signal only. A green run is review coverage, not approval; each finding must be verified against the current source.',
  ];
  if (stale) {
    body.push('', `⚠️ The PR moved to \`${currentHead.slice(0, 12)}\` while this review ran. No inline comments were posted for the stale head.`);
  }
  if (coverage !== 'complete_best_effort') {
    body.push('', '⚠️ Coverage was incomplete or could not be established. Zero findings must not be interpreted as a clean review.');
  } else if (findings.length === 0) {
    body.push('', 'No findings were reported for the selected files.');
  }
  if (unmapped.length > 0) {
    body.push('', '### Findings without a valid inline position');
    for (const finding of unmapped.slice(0, 25)) {
      const displayPath = sanitizedPath(finding.path);
      const location = finding.path
        ? `\`${displayPath}${finding.end ? `:${finding.end}` : ''}\``
        : '`unknown location`';
      body.push('', `- ${location}: ${finding.text.replace(/\n+/g, ' ').slice(0, 1200)}`);
    }
    if (unmapped.length > 25) {
      body.push('', `- ${unmapped.length - 25} additional findings are available in the redacted workflow artifact.`);
    }
  }
  if (coverage === 'failed' && diagnostic) {
    body.push('', '<details><summary>Sanitized diagnostic excerpt</summary>', '', '<pre>', diagnostic, '</pre>', '</details>');
  }
  body.push('', `[Workflow run](${runUrl})`);
  const summary = body.join('\n').slice(0, 65000);

  const issueComments = await github.paginate(github.rest.issues.listComments, {
    owner, repo, issue_number: pullNumber, per_page: 100,
  });
  const currentSummary = issueComments.find((comment) =>
    isWorkflowBot(comment)
      && typeof comment.body === 'string'
      && comment.body.includes(SUMMARY_MARKER));
  if (currentSummary) {
    await github.rest.issues.updateComment({
      owner, repo, comment_id: currentSummary.id, body: summary,
    });
  } else {
    await github.rest.issues.createComment({
      owner, repo, issue_number: pullNumber, body: summary,
    });
  }

  core.notice(`OCR coverage=${coverage}; findings=${findings.length}; inline=${postedInline}; already_posted=${alreadyPosted}; summarized=${unmapped.length}`);
  return {
    coverage,
    findings: findings.length,
    inline: postedInline,
    unmapped: unmapped.length,
    alreadyPosted,
  };
}

module.exports = {
  classifyCoverage,
  completeCoverageIsConsistent,
  extractStatuses,
  parseJsonOutput,
  postReview,
  validRightLines,
};
