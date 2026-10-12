package install

import (
	"fmt"
	"regexp"
	"strings"
)

const qwenWorkTokenUsageKey = "QODERCN_EXPOSE_TOKEN_USAGE"

var qwenWorkEnvAssignment = regexp.MustCompile(`^[ \t]*(?:export[ \t]+)?([A-Za-z_][A-Za-z0-9_.-]*)[ \t]*=[ \t]*`)

// Parse whole assignments, including multiline quoted values, before rewriting
// any bytes. A line-oriented replacement could corrupt an unrelated secret that
// happens to contain the flag's name. Unrecognized syntax is rejected safely.
func enableQwenWorkTokenUsage(body []byte) ([]byte, error) {
	s := string(body)
	if strings.ContainsRune(s, '\x00') {
		return nil, fmt.Errorf("environment contains a NUL byte")
	}
	var out strings.Builder
	pos, copied, found := 0, 0, false
	if strings.HasPrefix(s, "\xef\xbb\xbf") {
		pos = 3
	}
	for pos < len(s) {
		lineEnd := strings.IndexByte(s[pos:], '\n')
		if lineEnd < 0 {
			lineEnd = len(s)
		} else {
			lineEnd += pos
		}
		line := strings.TrimSpace(s[pos:lineEnd])
		if line == "" || strings.HasPrefix(line, "#") {
			pos = lineEnd + 1
			continue
		}
		match := qwenWorkEnvAssignment.FindStringSubmatchIndex(s[pos:lineEnd])
		if match == nil {
			return nil, fmt.Errorf("unsupported environment assignment")
		}
		key := s[pos+match[2] : pos+match[3]]
		start, end := pos+match[1], lineEnd
		if start < lineEnd && (s[start] == '\'' || s[start] == '"' || s[start] == '`') {
			quote := s[start]
			end = start + 1
			for end < len(s) {
				if s[end] == '\\' && end+1 < len(s) && s[end+1] == quote {
					end += 2
					continue
				}
				if s[end] == quote {
					break
				}
				end++
			}
			if end == len(s) {
				return nil, fmt.Errorf("unterminated quoted environment value")
			}
			end++
			lineEnd = end
			for lineEnd < len(s) && s[lineEnd] != '\n' {
				lineEnd++
			}
			tail := strings.TrimSpace(s[end:lineEnd])
			if tail != "" && !strings.HasPrefix(tail, "#") {
				return nil, fmt.Errorf("unexpected text after quoted environment value")
			}
		} else {
			if comment := strings.IndexByte(s[start:end], '#'); comment >= 0 {
				end = start + comment
			}
			for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r') {
				end--
			}
		}
		if key == qwenWorkTokenUsageKey {
			out.WriteString(s[copied:start])
			out.WriteByte('1')
			copied = end
			found = true
		}
		pos = lineEnd + 1
	}
	out.WriteString(s[copied:])
	if !found {
		newline := "\n"
		if strings.Contains(s, "\r\n") {
			newline = "\r\n"
		}
		if len(s) > 0 && s != "\xef\xbb\xbf" && !strings.HasSuffix(s, "\n") {
			out.WriteString(newline)
		}
		out.WriteString(qwenWorkTokenUsageKey + "=1" + newline)
	}
	return []byte(out.String()), nil
}
