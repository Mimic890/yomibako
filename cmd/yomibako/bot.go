package main

import (
	"flag"

	"github.com/Mimic890/yomibako/internal/bot"
	"github.com/Mimic890/yomibako/internal/logx"
)

func runBot(args []string) error {
	fs := flag.NewFlagSet("bot", flag.ContinueOnError)
	var c common
	c.register(fs)
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}

	cfg, registry, err := c.load()
	if err != nil {
		return err
	}
	st, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, cancel := contextWithSignals()
	defer cancel()

	logx.Infof("конфиг: база %s, book-dock %s, уровень логов %s", cfg.Database, cfg.BookDock, cfg.LogLevel)
	return bot.New(cfg, registry, st).Run(ctx)
}
