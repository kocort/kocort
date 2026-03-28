package heartbeat

import "testing"

func TestIsHeartbeatContentEffectivelyEmpty(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "blank", content: "", want: true},
		{name: "whitespace", content: "\n  \n\t", want: true},
		{name: "heading only", content: "# Heartbeat\n## Notes", want: true},
		{name: "empty checklist", content: "- [ ]\n* [x]\n+ ", want: true},
		{name: "actionable text", content: "# Heartbeat\nCheck the queue", want: false},
		{name: "hashtag is content", content: "#todo", want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsHeartbeatContentEffectivelyEmpty(tc.content); got != tc.want {
				t.Fatalf("IsHeartbeatContentEffectivelyEmpty() = %v, want %v", got, tc.want)
			}
		})
	}
}
