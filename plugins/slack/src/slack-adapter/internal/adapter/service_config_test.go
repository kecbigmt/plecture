package adapter

import (
	"strings"
	"testing"
)

func TestServiceConfigResolvesSeveralBotsAndRejectsAmbiguousChannels(t *testing.T) {
	t.Setenv("APP_ONE", "xapp-test-one")
	t.Setenv("BOT_ONE", "xoxb-test-one")
	t.Setenv("APP_TWO", "xapp-test-two")
	t.Setenv("BOT_TWO", "xoxb-test-two")
	input := []byte(`{"bots":[{"app_token_env":"APP_ONE","bot_token_env":"BOT_ONE","channel_id":"C1","allowed_user_ids":["U1"]},{"app_token_env":"APP_TWO","bot_token_env":"BOT_TWO","channel_id":"C2","allowed_user_ids":[]}]}`)
	bots, err := ParseServiceConfig(input)
	if err != nil || len(bots) != 2 || bots[0].AppToken != "xapp-test-one" || bots[1].BotToken != "xoxb-test-two" {
		t.Fatalf("bots = %+v, err = %v", bots, err)
	}
	if !bots[0].AllowsUser("U1") || bots[0].AllowsUser("U2") || !bots[1].AllowsUser("U2") {
		t.Fatal("allowed_user_ids was not applied per bot")
	}
	for _, tc := range []struct{ name, input, want string }{
		{"duplicate channel", strings.Replace(string(input), `"channel_id":"C2"`, `"channel_id":"C1"`, 1), "duplicate channel_id"},
		{"missing token", strings.Replace(string(input), `"app_token_env":"APP_TWO"`, `"app_token_env":"ABSENT"`, 1), "ABSENT"},
		{"unknown field", strings.Replace(string(input), `"channel_id":"C2"`, `"channel_id":"C2","secret":"value"`, 1), "unknown field"},
		{"invalid environment name", strings.Replace(string(input), `"app_token_env":"APP_TWO"`, `"app_token_env":"1APP"`, 1), "invalid token environment name"},
		{"invalid channel", strings.Replace(string(input), `"channel_id":"C2"`, `"channel_id":"c-two"`, 1), "invalid channel_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseServiceConfig([]byte(tc.input)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
