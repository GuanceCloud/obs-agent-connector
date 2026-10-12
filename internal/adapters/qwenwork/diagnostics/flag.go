package diagnostics

import (
	"io"
	"os"
	"regexp"
	"strings"
)

var assignment = regexp.MustCompile(`^[ \t]*(?:export[ \t]+)?([A-Za-z_][A-Za-z0-9_.-]*)[ \t]*=[ \t]*`)

// Flag recognizes dotenv assignments, including multiline unrelated values,
// without exposing any value. Match the SDK's trimmed, case-insensitive truthy
// values (1, true and yes), not just the installer's canonical value 1.
func Flag(path string) string {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "missing"
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return "unknown (unreadable or unsupported dotenv)"
	}
	f, err := os.Open(path)
	if err != nil {
		return "unknown (unreadable dotenv)"
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return "unknown (unreadable dotenv)"
	}
	s := strings.TrimPrefix(string(body), "\xef\xbb\xbf")
	if strings.ContainsRune(s, '\x00') {
		return "invalid"
	}
	result := "missing"
	for pos := 0; pos < len(s); {
		lineEnd := pos + strings.IndexByte(s[pos:], '\n')
		if lineEnd < pos {
			lineEnd = len(s)
		}
		line := strings.TrimSpace(s[pos:lineEnd])
		if line == "" || strings.HasPrefix(line, "#") {
			pos = lineEnd + 1
			continue
		}
		match := assignment.FindStringSubmatchIndex(s[pos:lineEnd])
		if match == nil {
			return "invalid"
		}
		key := s[pos+match[2] : pos+match[3]]
		start, end := pos+match[1], lineEnd
		value := ""
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
				return "invalid"
			}
			value = s[start+1 : end]
			end++
			lineEnd = end
			for lineEnd < len(s) && s[lineEnd] != '\n' {
				lineEnd++
			}
			tail := strings.TrimSpace(s[end:lineEnd])
			if tail != "" && !strings.HasPrefix(tail, "#") {
				return "invalid"
			}
		} else {
			value = s[start:end]
			if i := strings.IndexByte(value, '#'); i >= 0 {
				value = value[:i]
			}
			value = strings.TrimSpace(value)
		}
		if key == "QODERCN_EXPOSE_TOKEN_USAGE" {
			result = "disabled"
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "1", "true", "yes":
				result = "enabled"
			}
		}
		pos = lineEnd + 1
	}
	return result
}
