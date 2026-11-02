// Package rx : expressions régulières au comportement de Python `re` (lookbehind, \w Unicode),
// pour les règles écrites par l'utilisateur dans preview.toml et les motifs internes qui en dépendent.
package rx

import (
	"strings"

	"github.com/dlclark/regexp2"
)

// Re : une regex compilée deux fois, libre (Search) et ancrée au début (Match, comme re.match).
type Re struct {
	src    string
	free   *regexp2.Regexp
	anchor *regexp2.Regexp
}

// Compile compile un motif de syntaxe Python. Option : regexp2.Multiline, etc.
func Compile(pat string, opts ...regexp2.RegexOptions) (*Re, error) {
	var o regexp2.RegexOptions
	for _, x := range opts {
		o |= x
	}
	pat = pyToNet(pat)
	f, err := regexp2.Compile(pat, o)
	if err != nil {
		return nil, err
	}
	a, err := regexp2.Compile(`\G(?:`+pat+`)`, o)
	if err != nil {
		return nil, err
	}
	return &Re{src: pat, free: f, anchor: a}, nil
}

// MustCompile : pour les motifs internes.
func MustCompile(pat string, opts ...regexp2.RegexOptions) *Re {
	r, err := Compile(pat, opts...)
	if err != nil {
		panic(err)
	}
	return r
}

// pyToNet : (?P<nom>…) et (?P=nom) de Python vers la syntaxe .NET.
func pyToNet(p string) string {
	if !strings.Contains(p, "(?P") {
		return p
	}
	p = strings.ReplaceAll(p, "(?P<", "(?<")
	var b strings.Builder
	for {
		i := strings.Index(p, "(?P=")
		if i < 0 {
			b.WriteString(p)
			break
		}
		j := strings.IndexByte(p[i:], ')')
		if j < 0 {
			b.WriteString(p)
			break
		}
		b.WriteString(p[:i] + `\k<` + p[i+4:i+j] + ">")
		p = p[i+j+1:]
	}
	return b.String()
}

func (r *Re) String() string { return r.src }

// Match : comme re.match — groupes (0 = tout) ou nil.
func (r *Re) Match(s string) []string {
	m, err := r.anchor.FindStringMatch(s)
	if err != nil || m == nil {
		return nil
	}
	return groups(m)
}

// Search : comme re.search.
func (r *Re) Search(s string) []string {
	m, err := r.free.FindStringMatch(s)
	if err != nil || m == nil {
		return nil
	}
	return groups(m)
}

// MatchString : vrai si re.search trouve.
func (r *Re) MatchString(s string) bool {
	ok, _ := r.free.MatchString(s)
	return ok
}

// Loc : une occurrence avec ses positions (en runes) et ses groupes ; Set[i] = groupe i capturé.
type Loc struct {
	Start, End int // en runes
	Groups     []string
	Set        []bool
}

// FindAll : comme re.finditer ; positions en runes.
func (r *Re) FindAll(s string) []Loc {
	var out []Loc
	m, err := r.free.FindStringMatch(s)
	for err == nil && m != nil {
		gs := m.Groups()
		l := Loc{Start: m.Index, End: m.Index + m.Length}
		for _, g := range gs {
			l.Groups = append(l.Groups, g.String())
			l.Set = append(l.Set, len(g.Captures) > 0)
		}
		out = append(out, l)
		m, err = r.free.FindNextMatch(m)
	}
	return out
}

// Sub : comme re.sub avec une chaîne de remplacement littérale.
func (r *Re) Sub(s, repl string) string {
	out, err := r.free.ReplaceFunc(s, func(regexp2.Match) string { return repl }, -1, -1)
	if err != nil {
		return s
	}
	return out
}

func groups(m *regexp2.Match) []string {
	gs := m.Groups()
	out := make([]string, len(gs))
	for i, g := range gs {
		out[i] = g.String()
	}
	return out
}

// Escape : comme re.escape de Python ≥ 3.7.
func Escape(s string) string {
	const special = "()[]{}?*+-|^$\\.&~# \t\n\r\v\f"
	var b strings.Builder
	for _, c := range s {
		if strings.ContainsRune(special, c) {
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	return b.String()
}
