package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

func cmdImport(args []string) error {
	flags := newCommandFlags("import")
	overwrite := flags.Bool("overwrite", false, "")
	if err := flags.parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: ta import [-e ENV] [-overwrite] FILE   (use - for stdin)")
	}
	if flags.Arg(0) == "-" && isTerminal(os.Stdin) {
		return errors.New("refusing to read .env values from a terminal; pipe or redirect a file into `ta import -` instead")
	}
	// Fail before reading stdin when the vault has not been initialized.
	v, err := openVault()
	if err != nil {
		return err
	}
	data, err := readDotenvFile(flags.Arg(0))
	if err != nil {
		return err
	}
	imported, skipped, err := v.importDotenv(flags.env, data, *overwrite)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "imported %d secrets into %s; kept %d existing secrets\n", imported, flags.env, skipped)
	return nil
}

// readDotenvFile reads values directly into ta, never through a shell or output.
func readDotenvFile(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

// parseDotenv reads the same basic syntax as the web UI: export, quoted
// multiline values, double-quoted escapes, and whitespace-prefixed # comments.
// Later assignments win, but any invalid or empty assignment rejects the batch.
// Values are literal: no variable expansion or shell execution takes place.
// Errors deliberately omit names and source text, which may contain secrets.
func parseDotenv(data []byte) (map[string]string, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("input must be UTF-8")
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, errors.New("input must not contain NUL")
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(text, "\n")
	entries := map[string]string{}
	for i := 0; i < len(lines); i++ {
		lineNo := i + 1
		line := strings.TrimLeftFunc(lines[i], unicode.IsSpace)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") || strings.HasPrefix(line, "export\t") {
			line = strings.TrimLeftFunc(line[7:], unicode.IsSpace)
		}
		name, rest, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("line %d: expected NAME=VALUE", lineNo)
		}
		name = strings.TrimSpace(name)
		if !validName(name) {
			return nil, fmt.Errorf("line %d: invalid secret name", lineNo)
		}
		rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
		var value string
		if strings.HasPrefix(rest, "\"") || strings.HasPrefix(rest, "'") {
			var err error
			value, i, err = parseDotenvQuoted(lines, i, rest)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo, err)
			}
		} else {
			// A # at the start of a value, or after whitespace, starts a comment.
			for j, c := range rest {
				if c == '#' && (j == 0 || rest[j-1] == ' ' || rest[j-1] == '\t') {
					rest = rest[:j]
					break
				}
			}
			value = strings.TrimSpace(rest)
		}
		if value == "" {
			return nil, fmt.Errorf("line %d: empty value; nothing imported", lineNo)
		}
		entries[name] = value
	}
	if len(entries) == 0 {
		return nil, errors.New("no secrets in input; nothing imported")
	}
	return entries, nil
}

func parseDotenvQuoted(lines []string, start int, rest string) (string, int, error) {
	quote := rest[0]
	body := rest[1:]
	var value strings.Builder
	for i := start; ; i++ {
		for j := 0; j < len(body); j++ {
			c := body[j]
			if c == quote {
				tail := strings.TrimSpace(body[j+1:])
				if tail != "" && !strings.HasPrefix(tail, "#") {
					return "", i, errors.New("unexpected text after closing quote")
				}
				return value.String(), i, nil
			}
			if quote == '"' && c == '\\' && j+1 < len(body) {
				j++
				switch body[j] {
				case 'n':
					c = '\n'
				case 'r':
					c = '\r'
				case 't':
					c = '\t'
				case '"', '\\':
					c = body[j]
				default:
					value.WriteByte('\\')
					c = body[j]
				}
			}
			value.WriteByte(c)
		}
		if i+1 == len(lines) {
			return "", i, errors.New("missing closing quote")
		}
		value.WriteByte('\n')
		body = lines[i+1]
	}
}
