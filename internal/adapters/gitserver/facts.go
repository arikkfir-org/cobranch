package gitserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
)

// maxSettings is the largest /cobranch.yaml a push may bring into the default branch.
const maxSettings = 1 << 20

// pushFacts is what a push's transaction stores and checks of the repository's branches once the push applies, and the
// branches it was counted from.
type pushFacts struct {
	requirements []git.Requirements
	facts        []git.Facts
	tips         map[string]string
	brought      []git.Brought
}

// branchFacts works out what the push's transaction stores of the repository's branches once the push applies: the
// merge requirements of /cobranch.yaml at the default branch's new tip when the push moves that branch, and, when it
// moves any branch, every other live branch's facts against the default branch, with the tips they were counted from
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/branch-status.md, "Graph facts"), and what
// each move brings into its branch. A /cobranch.yaml that doesn't parse refuses the push: refusal says why.
func (s *Server) branchFacts(ctx context.Context, dir string, env []string, updates []git.RefUpdate) (out pushFacts, refusal string, err error) {
	moves := slices.ContainsFunc(updates, func(u git.RefUpdate) bool { _, ok := git.Branch(u.Name); return ok })
	if !moves {
		return pushFacts{}, "", nil
	}
	tips, err := s.branchTips(ctx, dir, updates)
	if err != nil {
		return pushFacts{}, "", err
	}
	out.tips = tips
	head, hasDefault := tips[git.DefaultBranch]
	if hasDefault {
		out.requirements, refusal, err = s.settings(ctx, dir, env, head, git.Push{Updates: updates}.MovesDefault())
		if refusal != "" || err != nil {
			return pushFacts{}, refusal, err
		}
	}
	refs, remove, err := s.tipsDir(dir, tips)
	if err != nil {
		return pushFacts{}, "", err
	}
	defer remove()
	if !hasDefault {
		out.facts, err = s.countAll(ctx, dir, env, tips)
	} else {
		out.facts, err = s.aheadBehind(ctx, refs, dir, env, tips, head, linearHistory(out.requirements))
	}
	if err != nil {
		return pushFacts{}, "", err
	}
	if out.brought, err = s.brought(ctx, refs, dir, env, tips, updates); err != nil {
		return pushFacts{}, "", err
	}
	return out, "", nil
}

// settings reads the merge requirements of /cobranch.yaml at the default branch's tip, head. One that doesn't parse
// refuses a push that moves the default branch, saying why; any other push keeps the requirements as they were, which
// named none, since Cobranch never read that file.
func (s *Server) settings(ctx context.Context, dir string, env []string, head string, movesDefault bool) ([]git.Requirements, string, error) {
	reqs, err := s.requirementsAt(ctx, dir, env, head)
	var bad *code.SettingsError
	switch {
	case errors.As(err, &bad) && movesDefault:
		return nil, bad.Error(), nil
	case errors.As(err, &bad):
		return nil, "", nil
	case err != nil:
		return nil, "", err
	}
	return reqs, "", nil
}

// brought works out what each of updates that moves a branch to a commit brings into it, over refs, the branches as
// they are once the push applies.
func (s *Server) brought(ctx context.Context, refs, dir string, env []string, tips map[string]string, updates []git.RefUpdate) ([]git.Brought, error) {
	var out []git.Brought
	for _, u := range updates {
		name, ok := git.Branch(u.Name)
		if !ok || u.New == git.ZeroSHA {
			continue
		}
		b, err := s.bringing(ctx, refs, dir, env, tips, name, u)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// bringing works out what u's move of branch name brings into it: the live branches, besides it and the default, whose
// tips its new tip holds and its start lacked, and, for a branch besides the default, the default lacks too; and how
// many of the commits it adds, merges aside, none of those nor the default branch holds. Its start is its old tip, or,
// for a branch it creates, the default branch's tip.
func (s *Server) bringing(ctx context.Context, refs, dir string, env []string, tips map[string]string, name string, u git.RefUpdate) (git.Brought, error) {
	main, hasDefault := tips[git.DefaultBranch]
	start := u.Old
	if start == git.ZeroSHA {
		start = ""
		if name != git.DefaultBranch && hasDefault {
			start = main
		}
	}
	list := []string{"--git-dir=" + refs, "for-each-ref", "--format=%(refname:lstrip=2) %(objectname)", "--merged=" + u.New}
	revs := []string{"--no-merges", u.New}
	if start != "" {
		list = append(list, "--no-merged="+start)
		revs = append(revs, "^"+start)
	}
	if hasDefault && name != git.DefaultBranch {
		// A branch already in the default brings only commits it holds, which never count against another branch.
		list = append(list, "--no-merged="+main)
		revs = append(revs, "^"+main)
	}
	listed, err := s.output(ctx, env, "", append(list, "refs/heads/")...)
	if err != nil {
		return git.Brought{}, err
	}
	b := git.Brought{Branch: name}
	for line := range strings.SplitSeq(strings.TrimSpace(listed), "\n") {
		branch, sha, ok := strings.Cut(line, " ")
		if !ok || branch == name || branch == git.DefaultBranch {
			continue
		}
		b.Heads = append(b.Heads, git.Head{Branch: branch, SHA: sha})
		revs = append(revs, "^"+sha)
	}
	if b.Orphans, err = s.count(ctx, dir, env, revs...); err != nil {
		return git.Brought{}, err
	}
	return b, nil
}

// linearHistory reports whether reqs require linear history of merges into the default branch.
func linearHistory(reqs []git.Requirements) bool {
	i := slices.IndexFunc(reqs, func(r git.Requirements) bool { return r.Branch == git.DefaultBranch })
	return i >= 0 && reqs[i].LinearHistory
}

// branchTips are the repository's live branches once the push applies, by name, each with the commit it names.
func (s *Server) branchTips(ctx context.Context, dir string, updates []git.RefUpdate) (map[string]string, error) {
	listed, err := s.output(ctx, nil, "", "--git-dir="+dir, "for-each-ref", "--format=%(objectname) %(refname)", "refs/heads/")
	if err != nil {
		return nil, err
	}
	tips := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(listed), "\n") {
		sha, ref, ok := strings.Cut(line, " ")
		if name, isBranch := git.Branch(ref); ok && isBranch {
			tips[name] = sha
		}
	}
	for _, u := range updates {
		name, ok := git.Branch(u.Name)
		switch {
		case !ok:
		case u.New == git.ZeroSHA:
			delete(tips, name)
		default:
			tips[name] = u.New
		}
	}
	return tips, nil
}

// requirementsAt reads the merge requirements of /cobranch.yaml at commit: none without the file, and a
// *code.SettingsError when it doesn't parse.
func (s *Server) requirementsAt(ctx context.Context, dir string, env []string, commit string) ([]git.Requirements, error) {
	path := commit + ":" + git.SettingsFile
	checked, err := s.output(ctx, env, path+"\n", "--git-dir="+dir, "cat-file", "--batch-check")
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(checked)
	switch {
	case len(fields) == 2 && fields[1] == "missing":
		return nil, nil
	case len(fields) != 3:
		return nil, fmt.Errorf("git cat-file printed %q for %s", strings.TrimSpace(checked), path)
	case fields[1] != "blob":
		return nil, &code.SettingsError{Message: "it must be a file"}
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil {
		return nil, fmt.Errorf("git cat-file printed %q for %s", strings.TrimSpace(checked), path)
	}
	if size > maxSettings {
		return nil, &code.SettingsError{Message: "it must be at most 1 MiB"}
	}
	content, err := s.output(ctx, env, "", "--git-dir="+dir, "cat-file", "blob", fields[0])
	if err != nil {
		return nil, err
	}
	reqs, err := code.ParseSettings([]byte(content))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return reqs, nil
}

// aheadBehind counts every live branch but the default against its tip, head, in one walk, as git for-each-ref's
// ahead-behind does, over refs, the branches as they are once the push applies; and, when linear is true, the merges
// among each one's commits that head lacks.
func (s *Server) aheadBehind(ctx context.Context, refs, dir string, env []string, tips map[string]string, head string, linear bool) ([]git.Facts, error) {
	listed, err := s.output(ctx, env, "", "--git-dir="+refs, "for-each-ref", "--format=%(refname:lstrip=2) %(ahead-behind:"+head+")",
		"refs/heads/")
	if err != nil {
		return nil, err
	}
	var facts []git.Facts
	for line := range strings.SplitSeq(strings.TrimSpace(listed), "\n") {
		f, err := parseAheadBehind(line)
		if err != nil {
			return nil, err
		}
		if f.Branch == git.DefaultBranch {
			continue
		}
		if linear && f.Ahead > 0 {
			merges, err := s.count(ctx, dir, env, "--merges", tips[f.Branch], "^"+head)
			if err != nil {
				return nil, err
			}
			f.Merges = &merges
		}
		facts = append(facts, f)
	}
	return facts, nil
}

// parseAheadBehind reads a line of for-each-ref's "<branch> <ahead> <behind>".
func parseAheadBehind(line string) (git.Facts, error) {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return git.Facts{}, fmt.Errorf("git for-each-ref printed %q", line)
	}
	ahead, aheadErr := countOf(fields[1])
	behind, behindErr := countOf(fields[2])
	if aheadErr != nil || behindErr != nil {
		return git.Facts{}, fmt.Errorf("git for-each-ref printed %q", line)
	}
	return git.Facts{Branch: fields[0], Ahead: ahead, Behind: behind}, nil
}

// countAll counts every live branch's commits, for a repository with no default branch to count them against.
func (s *Server) countAll(ctx context.Context, dir string, env []string, tips map[string]string) ([]git.Facts, error) {
	facts := make([]git.Facts, 0, len(tips))
	for name, tip := range tips {
		ahead, err := s.count(ctx, dir, env, tip)
		if err != nil {
			return nil, err
		}
		facts = append(facts, git.Facts{Branch: name, Ahead: ahead})
	}
	slices.SortFunc(facts, func(a, b git.Facts) int { return strings.Compare(a.Branch, b.Branch) })
	return facts, nil
}

// count counts the commits that git rev-list lists for revs.
func (s *Server) count(ctx context.Context, dir string, env []string, revs ...string) (int, error) {
	out, err := s.output(ctx, env, "", append([]string{"--git-dir=" + dir, "rev-list", "--count"}, revs...)...)
	if err != nil {
		return 0, err
	}
	n, err := countOf(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("git rev-list printed %q", strings.TrimSpace(out))
	}
	return n, nil
}

// tipsDir lays out a bare repository beside dir whose refs are the branches tips names and whose objects are dir's,
// so that one for-each-ref counts the branches as they are once the push applies: Git keeps a push's ref updates
// until its hook accepts them. It returns the directory and what removes it.
func (s *Server) tipsDir(dir string, tips map[string]string) (string, func(), error) {
	refs, err := os.MkdirTemp(filepath.Join(s.cfg.DataDir, workDir), "tips-")
	if err != nil {
		return "", nil, fmt.Errorf("making a repository of the branches: %w", err)
	}
	remove := func() { _ = os.RemoveAll(refs) } // the pod's end empties the directory anyway
	snapshot := git.Snapshot{}
	for name, sha := range tips {
		snapshot.Refs = append(snapshot.Refs, git.Ref{Name: "refs/heads/" + name, SHA: sha})
	}
	if err := layout(refs, snapshot, "[core]\n\trepositoryformatversion = 0\n\tbare = true\n"); err != nil {
		remove()
		return "", nil, err
	}
	alternates := filepath.Join(dir, "objects") + "\n"
	if err := os.WriteFile(filepath.Join(refs, "objects", "info", "alternates"), []byte(alternates), 0o600); err != nil {
		remove()
		return "", nil, fmt.Errorf("writing the alternates: %w", err)
	}
	return refs, remove, nil
}

// countOf reads a count Git printed, which the branches' rows keep as integers of 32 bits.
func countOf(s string) (int, error) {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("reading count %q: %w", s, err)
	}
	return int(n), nil
}
