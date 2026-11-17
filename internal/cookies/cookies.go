// Package cookies reads a Netscape cookies.txt and hands out a Cookie header per site, so every
// source can be authorised from a single file that the user exports once and re-sends when it
// expires.
package cookies

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Jar maps a bare domain ("ranobelib.me") to a Cookie header for it.
type Jar map[string]string

// Load parses a Netscape cookies.txt. Lines for unrelated sites are kept: one export can hold
// cookies for every source at once.
func Load(path string) (Jar, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cookies: %w", err)
	}
	return Parse(body)
}

// Parse builds a jar out of a cookies.txt body.
func Parse(body []byte) (Jar, error) {
	byDomain := make(map[string][]string)
	count := 0

	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, "#HttpOnly_")
		if strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		domain := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(fields[0]), "."))
		name := strings.TrimSpace(fields[5])
		value := fields[6]
		if domain == "" || name == "" {
			continue
		}
		byDomain[domain] = append(byDomain[domain], name+"="+value)
		count++
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("cookies: %w", err)
	}
	if count == 0 {
		return nil, fmt.Errorf("cookies: в файле нет ни одной строки в формате Netscape")
	}

	jar := make(Jar, len(byDomain))
	for domain, pairs := range byDomain {
		jar[domain] = strings.Join(pairs, "; ")
	}
	return jar, nil
}

// Merge folds a fresh export into the file on disk and returns the combined jar. Every domain the
// new export mentions is replaced whole - a stale access token next to a new one is worse than
// losing a cookie - while domains it does not mention are left untouched.
func Merge(path string, body []byte) (Jar, error) {
	incoming, err := Parse(body)
	if err != nil {
		return nil, err
	}

	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
	case os.IsNotExist(err):
		existing = nil
	default:
		return nil, fmt.Errorf("cookies: %w", err)
	}

	kept := make([]string, 0, 16)
	scanner := bufio.NewScanner(bytes.NewReader(existing))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		domain := lineDomain(line)
		if domain != "" {
			if _, replaced := incoming[domain]; replaced {
				continue
			}
		}
		kept = append(kept, line)
	}

	var merged strings.Builder
	for _, line := range kept {
		merged.WriteString(line)
		merged.WriteString("\n")
	}
	for _, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		merged.WriteString(line)
		merged.WriteString("\n")
	}

	// Written in place rather than through a rename: the config directory may be mounted
	// read-only for everything but this file.
	if err := os.WriteFile(path, []byte(merged.String()), 0o600); err != nil {
		return nil, fmt.Errorf("cookies: не смог записать %s: %w", path, err)
	}
	return Load(path)
}

// lineDomain returns the domain of a cookies.txt line, or an empty string for comments.
func lineDomain(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "#HttpOnly_") {
		return ""
	}
	trimmed = strings.TrimPrefix(trimmed, "#HttpOnly_")
	fields := strings.Split(trimmed, "\t")
	if len(fields) < 7 {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(fields[0]), "."))
}

// For returns the Cookie header that applies to a host. The exact domain wins; otherwise the
// closest parent domain that has cookies is used, so ".rulate.ru" also covers "tl.rulate.ru".
func (j Jar) For(host string) string {
	host = strings.ToLower(host)
	if header, ok := j[host]; ok {
		return header
	}

	parts := strings.Split(host, ".")
	for i := 1; i < len(parts)-1; i++ {
		if header, ok := j[strings.Join(parts[i:], ".")]; ok {
			return header
		}
	}
	return ""
}

// Domains lists what the jar holds, for logging without leaking values.
func (j Jar) Domains() []string {
	out := make([]string, 0, len(j))
	for domain := range j {
		out = append(out, domain)
	}
	sort.Strings(out)
	return out
}
