package main

import (
	"reflect"
	"testing"

	"github.com/disgoorg/snowflake/v2"
)

func TestGuildConfiguration(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  []snowflake.ID
	}{
		{"123456789012345678", []snowflake.ID{123456789012345678}},
		{" 123456789012345678, 234567890123456789,123456789012345678 ", []snowflake.ID{123456789012345678, 234567890123456789}},
	} {
		got, err := parseGuildIDs(tc.value)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("guild configuration: got %v, %v; want %v", got, err, tc.want)
		}
	}
	for _, value := range []string{"", "0", "123,,456", "123,", "abc", "-1", "18446744073709551616"} {
		if _, err := parseGuildIDs(value); err == nil {
			t.Fatal("invalid guild configuration accepted")
		}
	}
}
