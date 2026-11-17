// Package cookies reads a Netscape cookies.txt once and hands out a Cookie header per site, so
// every source can be authorised from a single file exported by the browser.
package cookies

import (
	"bufio"
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
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cookies: %w", err)
	}
	defer file.Close()

	byDomain := make(map[string][]string)

	scanner := bufio.NewScanner(file)
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
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("cookies: %w", err)
	}

	jar := make(Jar, len(byDomain))
	for domain, pairs := range byDomain {
		jar[domain] = strings.Join(pairs, "; ")
	}
	return jar, nil
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
