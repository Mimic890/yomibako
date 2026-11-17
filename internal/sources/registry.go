// Package sources holds every configured source and resolves a link to the one that serves it.
package sources

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Mimic890/yomibako/internal/config"
	"github.com/Mimic890/yomibako/internal/cookies"
	"github.com/Mimic890/yomibako/internal/source"
	"github.com/Mimic890/yomibako/internal/sources/comx"
	"github.com/Mimic890/yomibako/internal/sources/mangalib"
	"github.com/Mimic890/yomibako/internal/sources/ranobehub"
	"github.com/Mimic890/yomibako/internal/sources/ranobelib"
	"github.com/Mimic890/yomibako/internal/sources/rulate"
)

// ErrNotURL is returned when the input is not a full link to a title.
var ErrNotURL = errors.New("нужна полная ссылка на тайтл")

// Registry is the set of sources the service can read from.
type Registry struct {
	byName map[string]source.Source
	byHost map[string]string
	order  []string
	host   map[string]string
	label  map[string]string
}

// New builds the registry from the configuration.
func New(cfg config.Config) *Registry {
	registry := &Registry{
		byName: make(map[string]source.Source),
		byHost: make(map[string]string),
		host:   make(map[string]string),
		label:  make(map[string]string),
	}

	novel := ranobelib.New()
	applyClient(novel.Client, cfg)
	registry.add(novel, "RanobeLib", "ranobelib.me")

	hub := ranobehub.New()
	applyWeb(&hub.Client.UserAgent, &hub.Client.Interval, cfg)
	registry.add(hub, "RanobeHub", "ranobehub.org")

	rulateSource := rulate.New()
	applyWeb(&rulateSource.Client.UserAgent, &rulateSource.Client.Interval, cfg)
	registry.add(rulateSource, "TL.Rulate", "tl.rulate.ru", "rulate.ru")

	manga := mangalib.New()
	applyClient(manga.Client, cfg)
	registry.add(manga, "MangaLib", "mangalib.me")

	comxSource := comx.New()
	applyWeb(&comxSource.Client.UserAgent, &comxSource.Client.Interval, cfg)
	registry.add(comxSource, "Com-X", "com-x.life", "ru.com-x.life")

	hentai := mangalib.NewHentaiLib()
	applyClient(hentai.Client, cfg)
	registry.add(hentai, "HentaiLib", "hentailib.me")

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

func applyWeb(userAgent *string, interval *time.Duration, cfg config.Config) {
	if cfg.UserAgent != "" {
		*userAgent = cfg.UserAgent
	}
	if cfg.RequestInterval > 0 {
		*interval = time.Duration(cfg.RequestInterval)
	}
}

func (r *Registry) add(src source.Source, label string, hosts ...string) {
	r.byName[src.Name()] = src
	r.order = append(r.order, src.Name())
	r.label[src.Name()] = label
	r.host[src.Name()] = hosts[0]
	for _, host := range hosts {
		r.byHost[host] = src.Name()
	}
}

// All returns every source in a stable order.
func (r *Registry) All() []source.Source {
	out := make([]source.Source, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.byName[name])
	}
	return out
}

// Novels returns the sources that produce EPUBs.
func (r *Registry) Novels() []source.Source { return r.byKind(source.KindNovel) }

// Manga returns the sources that produce CBZ files.
func (r *Registry) Manga() []source.Source { return r.byKind(source.KindManga) }

func (r *Registry) byKind(kind source.Kind) []source.Source {
	out := make([]source.Source, 0, len(r.order))
	for _, name := range r.order {
		if r.byName[name].Kind() == kind {
			out = append(out, r.byName[name])
		}
	}
	return out
}

// ByName returns a source by its name.
func (r *Registry) ByName(name string) (source.Source, bool) {
	src, ok := r.byName[name]
	return src, ok
}

// Label is the human-readable name of a source.
func (r *Registry) Label(name string) string {
	if label, ok := r.label[name]; ok {
		return label
	}
	return name
}

// Host returns the main host of a source, for messages.
func (r *Registry) Host(name string) string { return r.host[name] }

// SetCookies gives every source the cookies that belong to its host.
func (r *Registry) SetCookies(jar cookies.Jar) {
	for name, src := range r.byName {
		header := jar.For(r.host[name])
		if header == "" {
			continue
		}
		if setter, ok := src.(interface{ SetCookies(string) }); ok {
			setter.SetCookies(header)
		}
	}
}

// ReloadCookies forgets every cookie and applies the jar again, which is what a fresh upload from
// the bot needs: a domain that disappeared from the file must stop being sent.
func (r *Registry) ReloadCookies(jar cookies.Jar) {
	for _, src := range r.byName {
		if setter, ok := src.(interface{ SetCookies(string) }); ok {
			setter.SetCookies("")
		}
	}
	r.SetCookies(jar)
}

// SetToken applies a new access token to every source that supports one.
func (r *Registry) SetToken(token string) {
	for _, src := range r.byName {
		if setter, ok := src.(interface{ SetToken(string) }); ok {
			setter.SetToken(token)
		}
	}
}

// Resolve turns a link into a source and a slug. Only full links are accepted: picking a title is
// the user's job, the service never searches for one.
func (r *Registry) Resolve(input string) (source.Source, string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, "", fmt.Errorf("пустая ссылка")
	}
	if !strings.Contains(input, "://") {
		return nil, "", ErrNotURL
	}

	parsed, err := url.Parse(input)
	if err != nil {
		return nil, "", fmt.Errorf("не разобрал ссылку: %w", err)
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	name, ok := r.byHost[host]
	if !ok {
		return nil, "", fmt.Errorf("неизвестный сайт %q", host)
	}
	slug := slugFromPath(parsed.Path)
	if slug == "" {
		return nil, "", fmt.Errorf("в ссылке нет тайтла: %s", input)
	}
	return r.byName[name], slug, nil
}

// ResolveAs pairs a slug with a source the user picked by hand.
func (r *Registry) ResolveAs(name, slug string) (source.Source, string, error) {
	src, ok := r.byName[name]
	if !ok {
		return nil, "", fmt.Errorf("неизвестный источник %q", name)
	}
	return src, strings.Trim(strings.TrimSpace(slug), "/"), nil
}

// slugFromPath extracts the title part of a site path:
//
//	/ru/book/20818--lord-of-the-mysteries -> 20818--lord-of-the-mysteries
//	/book/102204                          -> 102204
//	/ranobe/1034-bleach-cant-fear-your-own -> 1034-bleach-cant-fear-your-own
func slugFromPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	for i, part := range parts {
		if part == "book" && i+1 < len(parts) {
			return strings.Split(parts[i+1], "?")[0]
		}
	}
	return strings.Split(parts[len(parts)-1], "?")[0]
}
