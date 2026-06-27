package i18nx

import (
	"embed"
	"log/slog"
	"sync"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/pelletier/go-toml/v2"
	"golang.org/x/text/language"
)

//go:embed locales/*.toml
var messageFiles embed.FS

var (
	bundleOnce sync.Once
	bundle     *i18n.Bundle
)

func Bundle() *i18n.Bundle {
	bundleOnce.Do(func() {
		b := i18n.NewBundle(language.SimplifiedChinese)
		b.RegisterUnmarshalFunc("toml", toml.Unmarshal)
		for _, name := range []string{
			"locales/active.zh-CN.toml",
			"locales/active.en-US.toml",
		} {
			if _, err := b.LoadMessageFileFS(messageFiles, name); err != nil {
				slog.Error("load i18n message file failed", "file", name, "err", err)
			}
		}
		bundle = b
	})
	return bundle
}
