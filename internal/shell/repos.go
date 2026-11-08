package shell

import (
	"fmt"
	"github.com/mrYush/hint/internal/rpc"
)

// ParseRepos parses arguments after /repos, already split by SplitWords.
func ParseRepos(sessionID string, args []string) (rpc.RepoRequest, error) {
	req := rpc.RepoRequest{SessionID: sessionID, Action: "list"}
	if len(args) == 0 {
		return req, nil
	}
	req.Action = args[0]
	args = args[1:]
	switch req.Action {
	case "list":
		if len(args) == 0 {
			return req, nil
		}
		if len(args) == 1 {
			req.Cursor = args[0]
			return req, nil
		}
	case "index":
		if len(args) == 0 {
			return req, nil
		}
		if len(args) == 1 && (args[0] == "off" || args[0] == "auto" || args[0] == "on") {
			req.Mode = args[0]
			return req, nil
		}
	case "add":
		if len(args) == 1 || len(args) == 2 {
			req.Path = args[0]
			if len(args) == 2 {
				req.Alias = args[1]
			}
			return req, nil
		}
	case "remove":
		if len(args) == 1 {
			req.Repo = args[0]
			return req, nil
		}
	case "clone":
		if len(args) == 2 || len(args) == 3 {
			req.URL = args[0]
			req.Path = args[1]
			if len(args) == 3 {
				req.Alias = args[2]
			}
			return req, nil
		}
	case "tree":
		if len(args) <= 2 {
			if len(args) > 0 {
				req.Repo = args[0]
			}
			if len(args) > 1 {
				req.Path = args[1]
			}
			return req, nil
		}
	case "next":
		if len(args) == 1 {
			req.Action = "tree"
			req.Cursor = args[0]
			return req, nil
		}
	}
	return rpc.RepoRequest{}, fmt.Errorf("usage: /repos [list | add <path> [alias] | remove <repo> | clone <URL> <new-path> [alias] | tree [repo] [path] | next <cursor> | index [off|auto|on]]")
}
