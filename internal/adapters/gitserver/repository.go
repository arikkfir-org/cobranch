package gitserver

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/repos"
)

// workspace builds a throwaway bare repository of repo from snapshot: its refs in packed-refs, its packs hard-linked
// from the cache, and HEAD at main. It returns the repository's directory and what removes it, which
// the request calls when it ends.
func (s *Server) workspace(ctx context.Context, repo repos.Repository, snapshot git.Snapshot) (string, func(), error) {
	dir, err := os.MkdirTemp(filepath.Join(s.cfg.DataDir, workDir), "repo-")
	if err != nil {
		return "", nil, fmt.Errorf("making a throwaway repository: %w", err)
	}
	remove := func() { _ = os.RemoveAll(dir) } // the pod's end empties the directory anyway
	if err := layout(dir, snapshot, s.repoConfig()); err != nil {
		remove()
		return "", nil, err
	}
	for _, pack := range snapshot.Packs {
		if err := s.cache.link(ctx, repo.ID, pack, filepath.Join(dir, "objects", "pack")); err != nil {
			remove()
			return "", nil, err
		}
	}
	return dir, remove, nil
}

// layout writes a bare repository's files into dir: its configuration, HEAD and the snapshot's refs in packed-refs.
func layout(dir string, snapshot git.Snapshot, config string) error {
	for _, sub := range []string{"objects/pack", "objects/info", "refs/heads", "refs/tags"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return fmt.Errorf("laying the repository out: %w", err)
		}
	}
	refs := slices.Clone(snapshot.Refs)
	// Git reads packed-refs sorted by name, byte by byte; the database's collation may sort otherwise.
	slices.SortFunc(refs, func(a, b git.Ref) int { return strings.Compare(a.Name, b.Name) })
	var packed strings.Builder
	packed.WriteString("# pack-refs with: sorted \n")
	for _, r := range refs {
		packed.WriteString(r.SHA + " " + r.Name + "\n")
	}
	files := map[string]string{
		"HEAD":        "ref: refs/heads/" + git.DefaultBranch + "\n",
		"config":      config,
		"packed-refs": packed.String(),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}
	return nil
}

// repoConfig is a throwaway repository's configuration: a fetch may want any object, as its advertisement may come
// from another snapshot; a push keeps its objects as one pack, checked, under the size limit, and runs the hook.
func (s *Server) repoConfig() string {
	return strings.Join([]string{
		"[core]",
		"\trepositoryformatversion = 0",
		"\tbare = true",
		"\tlogAllRefUpdates = false",
		"\thooksPath = " + filepath.Join(s.cfg.DataDir, hooksDir),
		"[gc]",
		"\tauto = 0",
		"[uploadpack]",
		"\tallowAnySHA1InWant = true",
		"[receive]",
		"\tunpackLimit = 1",
		"\tautogc = false",
		"\tfsckObjects = true",
		"\tmaxInputSize = " + strconv.FormatInt(s.cfg.MaxPushSize, 10),
		"",
	}, "\n")
}

// git returns the git CLI's command for args, isolated from the pod's own configuration, with extra variables.
func (s *Server) git(ctx context.Context, extra []string, args ...string) *exec.Cmd {
	return gitCommand(ctx, s.cfg.Git, filepath.Join(s.cfg.DataDir, workDir), extra, args...)
}

// gitCommand returns the command that runs the git CLI at bin for args, isolated from the pod's own configuration
// with home as its HOME, with extra variables.
func gitCommand(ctx context.Context, bin, home string, extra []string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	}, extra...)
	return cmd
}
