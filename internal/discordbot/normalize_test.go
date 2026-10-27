package discordbot

import (
	"strings"
	"testing"
)

func TestNormalizeAndRuneLimit(t *testing.T) {
	got := Normalize("**こんにちは** <@123> https://example.com/a <:smile:456>\n<@&789>", map[string]string{"123": "太郎"})
	if got != "こんにちは 太郎 リンク smile ロール" {
		t.Fatal(got)
	}
	if got = Normalize(strings.Repeat("あ", 201), nil); len([]rune(got)) != 200 {
		t.Fatal("rune limit")
	}
	if got = Normalize("\n** ` `**", nil); got != "" {
		t.Fatal("empty content was not removed")
	}
}
