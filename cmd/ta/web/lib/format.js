// Names are shell variable names; environments are also URL path segments
// and directory names. The patterns match the CLI's validName and validSlug.
export const NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
export const ENV_RE = /^[A-Za-z0-9][A-Za-z0-9_-]*$/;
export const NAME_PATTERN = "[A-Za-z_][A-Za-z0-9_]*";
export const ENV_PATTERN = "[A-Za-z0-9][A-Za-z0-9_\\-]*";
export const NAME_RULE = "Letters, digits and _, not starting with a digit";
export const ENV_RULE = "Letters, digits, _ and -, starting with a letter or digit";
export const NAME_ERROR = "Names use letters, digits and _, not starting with a digit.";
export const ENV_ERROR = "Environments use letters, digits, _ and -, starting with a letter or digit.";

export const DEFAULT_ENV = "default";

export const plural = (n, word) => `${n} ${word}${n === 1 ? "" : "s"}`;

export function ago(iso) {
  const s = (Date.now() - new Date(iso).getTime()) / 1000;
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  if (s < 30 * 86400) return `${Math.floor(s / 86400)}d ago`;
  return new Date(iso).toLocaleDateString();
}

// runCommand is the `ta run` prefix that injects one secret.
export function runCommand(env, name) {
  return env === DEFAULT_ENV ? `ta run -s ${name} -- ` : `ta run -e ${env} -s ${name} -- `;
}
