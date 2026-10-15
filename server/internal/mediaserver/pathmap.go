package mediaserver

import (
	"sort"
	"strings"
)

// pathMap rewrites path prefixes (config path_map): the longest matching
// prefix wins, and a prefix only matches at a path-segment boundary, so
// "/media" matches "/media" and "/media/tv" but not "/media2".
type pathMap struct {
	rules []rule // longest prefix first
}

type rule struct{ from, to string }

func newPathMap(m map[string]string) pathMap {
	var p pathMap
	for from, to := range m {
		p.rules = append(p.rules, rule{from: trimSlash(from), to: trimSlash(to)})
	}
	sort.Slice(p.rules, func(i, j int) bool {
		if len(p.rules[i].from) != len(p.rules[j].from) {
			return len(p.rules[i].from) > len(p.rules[j].from)
		}
		return p.rules[i].from < p.rules[j].from
	})
	return p
}

// trimSlash removes trailing slashes, so "/" becomes "".
func trimSlash(s string) string { return strings.TrimRight(s, "/") }

// apply returns path with the first (longest) matching prefix replaced, or
// path unchanged.
func (p pathMap) apply(path string) string {
	for _, r := range p.rules {
		if path != r.from && !strings.HasPrefix(path, r.from+"/") {
			continue
		}
		rest := path[len(r.from):]
		if strings.Contains(r.to, `\`) && !strings.Contains(r.to, "/") {
			rest = strings.ReplaceAll(rest, "/", `\`) // a Windows media server
		}
		if out := r.to + rest; out != "" {
			return out
		}
		return "/"
	}
	return path
}
