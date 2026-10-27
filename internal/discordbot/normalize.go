package discordbot

import (
	"regexp"
	"strings"
	"unicode"
)

var urls = regexp.MustCompile(`https?://[^\s<>]+`)
var users = regexp.MustCompile(`<@!?([0-9]+)>`)
var roles = regexp.MustCompile(`<@&[0-9]+>`)
var channels = regexp.MustCompile(`<#[0-9]+>`)
var emoji = regexp.MustCompile(`<a?:([A-Za-z0-9_]+):[0-9]+>`)

func Normalize(text string, names map[string]string) string {
	text = urls.ReplaceAllString(text, "リンク")
	text = users.ReplaceAllStringFunc(text, func(m string) string {
		id := users.FindStringSubmatch(m)[1]
		if name := names[id]; name != "" {
			return name
		}
		return "ユーザー"
	})
	text = roles.ReplaceAllString(text, "ロール")
	text = channels.ReplaceAllString(text, "チャンネル")
	text = emoji.ReplaceAllString(text, "$1")
	text = strings.Map(func(r rune) rune {
		if strings.ContainsRune("*_~`|", r) {
			return -1
		}
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > 200 {
		text = string(runes[:200])
	}
	return text
}
