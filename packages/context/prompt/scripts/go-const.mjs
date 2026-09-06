/**
 * Evaluate a Go string constant expression: a `+`-joined sequence of raw
 * strings, interpreted strings, and references to other string constants.
 *
 * Swarm's prompt constants embed markdown code spans, so they cannot be a
 * single raw string -- upstream splices backticks in as `+ "`" +` fragments.
 * A naive "text between the first pair of backticks" reader silently truncates
 * at the first code span, so this walks the whole expression.
 */
export function parseGoStringConst(source, name, seen = new Set()) {
  if (seen.has(name)) throw new Error(`cyclic constant reference at ${name}`);
  seen.add(name);

  const marker = `const ${name} =`;
  const at = source.indexOf(marker);
  if (at < 0) throw new Error(`Go constant ${name} was not found`);

  let cursor = at + marker.length;
  const fragments = [];

  for (;;) {
    while (/\s/.test(source[cursor] ?? "")) cursor++;
    const ch = source[cursor];

    if (ch === "`") {
      const end = source.indexOf("`", cursor + 1);
      if (end < 0) throw new Error(`unterminated raw string in ${name}`);
      fragments.push(source.slice(cursor + 1, end));
      cursor = end + 1;
    } else if (ch === '"') {
      let end = cursor + 1;
      for (; end < source.length; end++) {
        if (source[end] === "\\") { end++; continue; }
        if (source[end] === '"') break;
      }
      if (end >= source.length) throw new Error(`unterminated quoted string in ${name}`);
      fragments.push(JSON.parse(source.slice(cursor, end + 1)));
      cursor = end + 1;
    } else if (/[A-Za-z_]/.test(ch ?? "")) {
      const identifier = source.slice(cursor).match(/^[A-Za-z_][A-Za-z0-9_]*/)[0];
      fragments.push(parseGoStringConst(source, identifier, new Set(seen)));
      cursor += identifier.length;
    } else {
      throw new Error(`unexpected token ${JSON.stringify(ch ?? "<eof>")} in ${name}`);
    }

    // Fragments continue only across an explicit `+`. Requiring the operator
    // stops the walk cleanly at end-of-expression instead of running on into
    // whatever declaration happens to follow.
    let look = cursor;
    while (/\s/.test(source[look] ?? "")) look++;
    if (source[look] === "+") { cursor = look + 1; continue; }
    return fragments.join("");
  }
}
