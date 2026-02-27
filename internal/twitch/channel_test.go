package twitch

import "testing"

func TestNormalizeChannelLogin(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "plain login", in: "Squeex", want: "squeex"},
		{name: "at login", in: "@RocketLeague", want: "rocketleague"},
		{name: "host path", in: "twitch.tv/riotgames", want: "riotgames"},
		{name: "https url", in: "https://twitch.tv/LoL", want: "lol"},
		{name: "https www url", in: "https://www.twitch.tv/esl_csgo", want: "esl_csgo"},
		{name: "empty", in: "", wantErr: true},
		{name: "bad host", in: "https://example.com/foo", wantErr: true},
		{name: "bad char", in: "bad-name", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeChannelLogin(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NormalizeChannelLogin(%q) expected error", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeChannelLogin(%q) error = %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeChannelLogin(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
