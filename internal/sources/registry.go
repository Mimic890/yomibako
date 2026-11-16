// Package sources holds every configured source and resolves user input to one of them.
package sources

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Mimic890/yomibako/internal/config"
	"github.com/Mimic890/yomibako/internal/source"
	"github.com/Mimic890/yomibako/internal/sources/mangalib"
	"github.com/Mimic890/yomibako/internal/sources/ranobelib"
)

// Registry is the set of sources the service can read from.
type Registry struct {
	byName map[string]source.Source
	byHost map[string]string
	order  []string
}

// New builds the registry from the configuration.
func New(cfg config.Config) *Registry {
	registry := &Registry{byName: make(map[string]source.Source), byHost: make(map[string]string)}

	novel := ranobelib.New()
	applyClient(novel.Client, cfg)
	registry.add(novel, "ranobelib.me")

	manga := mangalib.New()
	applyClient(manga.Client, cfg)
	registry.add(manga, "mangalib.me")

	hentai := mangalib.NewHentaiLib()
	applyClient(hentai.Client, cfg)
	registry.add(hentai, "hentailib.me")

	return registry
}

func applyClient(client *ranobelib.Client, cfg config.Config) {
	if cfg.APIBase != "" {
		client.APIBase = cfg.APIBase
	}
	if cfg.UserAgent != "" {
		client.UserAgent = cfg.UserAgent
	}
	if cfg.RequestInterval > 0 {
		client.Interval = time.Duration(cfg.RequestInterval)
	}
	if cfg.Auth.Token != "" {
		client.Token = cfg.Auth.Token
	}
}

func (r *Registry) add(src source.Source, host string) {
	r.byName[src.Name()] = src
	r.byHost[host] = src.Name()
	r.order = append(r.order, src.Name())
}

// All returns every source in a stable order.
func (r *Registry) All() []source.Source {
	out := make([]source.Source, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.byName[name])
	}
	return out
}

// ByName returns a source by its name.
func (r *Registry) ByName(name string) (source.Source, bool) {
	src, ok := r.byName[name]
	return src, ok
}

// Default returns the source used when the input names none.
func (r *Registry) Default() source.Source { return r.byName["ranobelib"] }

// SetCookies applies a Cookie header to every source.
func (r *Registry) SetCookies(cookies string) {
	for _, src := range r.byName {
		switch typed := src.(type) {
		case *ranobelib.Source:
			typed.Client.Cookies = cookies
		case *mangalib.Source:
			typed.Client.Cookies = cookies
		}
	}
}

// SetToken applies a new access token to every source that supports one.
func (r *Registry) SetToken(token string) {
	for _, src := range r.byName {
		if setter, ok := src.(interface{ SetToken(string) }); ok {
			setter.SetToken(token)
		}
	}
}

// Resolve turns user input into a source and a slug. Accepted forms:
//
//	<slug>                       the default source
//	<name>:<slug>                a named source, for example mangalib:4464--jujutsu-kaisen
//	https://mangalib.me/ru/...   a link, matched by host
func (r *Registry) Resolve(input string) (source.Source, string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, "", fmt.Errorf("пустой запрос")
	}

	if strings.Contains(input, "://") || strings.Contains(input, "/") {
		parsed, err := url.Parse(input)
		if err != nil {
			return nil, "", fmt.Errorf("не разобрал ссылку: %w", err)
		}
		host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
		name, ok := r.byHost[host]
		if !ok {
			return nil, "", fmt.Errorf("неизвестный сайт %q", host)
		}
		return r.byName[name], slugFromPath(parsed.Path), nil
	}

	if index := strings.Index(input, ":"); index > 0 {
		name := input[:index]
		if src, ok := r.byName[name]; ok {
			return src, input[index+1:], nil
		}
	}

	return r.Default(), input, nil
}

// slugFromPath extracts the slug from a site path such as /ru/book/20818--lord-of-the-mysteries.
func slugFromPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i, part := range parts {
		if part == "book" && i+1 < len(parts) {
			return strings.Split(parts[i+1], "?")[0]
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Split(parts[len(parts)-1], "?")[0]
}
