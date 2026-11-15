package ranobelib

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadCookies reads a Netscape cookies.txt export and returns a Cookie header value for the
// RanobeLib domains. Lines that are comments, blank, or belong to other sites are skipped.
func LoadCookies(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var pairs []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		domain, name, value := fields[0], fields[5], fields[6]
		if !strings.Contains(domain, "cdnlibs.org") && !strings.Contains(domain, "ranobelib.me") {
			continue
		}
		pairs = append(pairs, name+"="+value)
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if len(pairs) == 0 {
		return "", fmt.Errorf("%s: no RanobeLib cookies found", path)
	}
	return strings.Join(pairs, "; "), nil
}
