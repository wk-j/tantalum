import { NAME_RE } from "./format.js";

// parseDotenv reads KEY=VALUE lines: blank lines and # comments are skipped,
// `export ` is allowed, "double" quotes understand \n \r \t \" \\ and may span
// lines, 'single' quotes are literal, and an unquoted value ends at " #".
// A later line wins over an earlier one with the same name.
export function parseDotenv(text) {
  const entries = new Map();
  const errors = [];
  const lines = text.replace(/\r\n?/g, "\n").split("\n");
  for (let i = 0; i < lines.length; i++) {
    const lineNo = i + 1;
    let line = lines[i].trim();
    if (!line || line.startsWith("#")) continue;
    if (line.startsWith("export ")) line = line.slice(7).trimStart();
    const eq = line.indexOf("=");
    if (eq < 1) {
      errors.push(`Line ${lineNo}: expected NAME=VALUE.`);
      continue;
    }
    const name = line.slice(0, eq).trim();
    if (!NAME_RE.test(name)) {
      errors.push(`Line ${lineNo}: ${name} is not a valid name.`);
      continue;
    }
    const rest = line.slice(eq + 1).trimStart();
    const quote = rest[0];
    let value;
    if (quote === '"' || quote === "'") {
      let body = rest.slice(1);
      let end = closingQuote(body, quote);
      while (end < 0 && i + 1 < lines.length) {
        body += "\n" + lines[++i];
        end = closingQuote(body, quote);
      }
      if (end < 0) {
        errors.push(`Line ${lineNo}: ${name} has no closing ${quote}.`);
        continue;
      }
      value = body.slice(0, end);
      if (quote === '"') {
        value = value.replace(/\\([nrt"\\])/g, (_, c) => ({ n: "\n", r: "\r", t: "\t" })[c] ?? c);
      }
    } else {
      const comment = rest.search(/\s#/);
      value = (comment >= 0 ? rest.slice(0, comment) : rest).trimEnd();
    }
    if (!value) {
      errors.push(`Line ${lineNo}: ${name} is empty and is skipped.`);
      continue;
    }
    entries.set(name, value);
  }
  return { entries, errors };
}

function closingQuote(s, quote) {
  for (let i = 0; i < s.length; i++) {
    if (quote === '"' && s[i] === "\\") i++;
    else if (s[i] === quote) return i;
  }
  return -1;
}
