package servers

import "testing"

func TestParseListLine(t *testing.T) {
	tests := []struct {
		name   string
		line   string
		online int
		max    int
		names  []string
		ok     bool
	}{
		{
			name:   "vanilla zero players",
			line:   "[12:00:00] [Server thread/INFO]: There are 0 of a max of 20 players online",
			online: 0, max: 20, ok: true,
		},
		{
			name:   "paper with players",
			line:   "[12:00:00] [Server thread/INFO]: There are 2 of a max of 20 players online: Steve, Alex_99",
			online: 2, max: 20, names: []string{"Steve", "Alex_99"}, ok: true,
		},
		{
			name:   "no log prefix",
			line:   "There are 1 of a max of 5 players online: Bob",
			online: 1, max: 5, names: []string{"Bob"}, ok: true,
		},
		{name: "unrelated line", line: "[12:00:00] [Server thread/INFO]: Done (12.345s)! For help, type \"help\"", ok: false},
		{name: "empty", line: "", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseListLine(tt.line)
			if ok != tt.ok {
				t.Fatalf("parseListLine(%q) ok = %v, want %v", tt.line, ok, tt.ok)
			}
			if !ok {
				return
			}
			if got.Online != tt.online || got.Max != tt.max {
				t.Errorf("online/max = %d/%d, want %d/%d", got.Online, got.Max, tt.online, tt.max)
			}
			if len(got.Names) != len(tt.names) {
				t.Fatalf("names = %v, want %v", got.Names, tt.names)
			}
			for i := range tt.names {
				if got.Names[i] != tt.names[i] {
					t.Fatalf("names = %v, want %v", got.Names, tt.names)
				}
			}
		})
	}
}
