package shell

import "testing"

func TestParseRepos(t *testing.T) {
	for _, tc := range []struct {
		input, action, path string
		invalid             bool
	}{
		{"/repos index", "index", "", false}, {"/repos index on", "index", "", false}, {"/repos index invalid", "", "", true}, {"/repos list token", "list", "", false},
		{"/repos", "list", "", false}, {"/repos add '/with space' lib", "add", "/with space", false}, {"/repos remove lib", "remove", "", false}, {"/repos clone https://example.org/a.git ./new lib", "clone", "./new", false}, {"/repos next token", "tree", "", false}, {"/repos tree lib src", "tree", "src", false}, {"/repos remove", "", "", true}, {"/repos add a b c", "", "", true}, {"/repos list extra too", "", "", true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			args, err := SplitWords(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			req, err := ParseRepos("session", args[1:])
			if (err != nil) != tc.invalid {
				t.Fatalf("%+v %v", req, err)
			}
			if !tc.invalid && (req.Action != tc.action || req.Path != tc.path || req.SessionID != "session") {
				t.Fatalf("wrong request: %+v", req)
			}
		})
	}
}
