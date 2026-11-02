package gitserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"

	"cobranch.dev/internal/adapters/gitinternal"
	"cobranch.dev/internal/services/code"
)

// diffArgs are the options of every git diff the Git server runs: renames found, and nothing of a user's
// configuration that changes what it prints.
var diffArgs = []string{"--find-renames", "--no-color", "--no-ext-diff", "--no-textconv", "--unified=3"}

// readDiff answers the change to the request's commit from the commit its base names, or from the empty tree without
// one, or, when it asks for the merge base, from the merge base of the two: the files Git's raw output lists, with
// their counts, and their hunks up to the limits of a diff.
func (s *Server) readDiff(w http.ResponseWriter, r *http.Request, o objects) error {
	ctx := r.Context()
	baseRef, mergeBase := r.URL.Query().Get("base"), r.URL.Query().Get("mergeBase") == "true"
	if (baseRef != "" && code.ValidateRef(baseRef) != nil) || (mergeBase && baseRef == "") {
		problem(w, http.StatusBadRequest, gitinternal.Problem{Message: "base must be a ref the API takes, and mergeBase needs one"})
		return nil
	}
	d, err := s.diffEnds(ctx, o, baseRef, mergeBase)
	if err != nil {
		return err
	}
	from := d.Base
	if from == "" {
		if from, err = s.emptyTree(ctx, o.dir); err != nil {
			return err
		}
	}
	files, err := s.diffFiles(ctx, o.dir, from, o.commit)
	if err != nil {
		return err
	}
	for _, f := range files {
		d.Additions += f.Additions
		d.Deletions += f.Deletions
	}
	if len(files) > code.MaxDiffFiles {
		d.FilesLeftOut, files = len(files)-code.MaxDiffFiles, files[:code.MaxDiffFiles]
	}
	if d.Files, err = s.diffHunks(ctx, o.dir, from, o.commit, files); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(gitinternal.DiffOf(d)) // a caller gone away gets nothing more
	return nil
}

// diffEnds resolves the commits a diff runs between: its head, the request's commit, and its base, the commit
// baseRef names, or, when mergeBase, the merge base of that commit, its tip, and the head; none when the two share no
// history.
func (s *Server) diffEnds(ctx context.Context, o objects, baseRef string, mergeBase bool) (code.Diff, error) {
	d := code.Diff{Head: o.commit}
	if baseRef == "" {
		return d, nil
	}
	tip, err := s.resolve(ctx, o.dir, baseRef)
	if err != nil {
		return code.Diff{}, err
	}
	d.Base = tip
	if mergeBase {
		d.BaseTip = tip
		if d.Base, err = s.mergeBase(ctx, o.dir, tip, o.commit); err != nil {
			return code.Diff{}, err
		}
	}
	return d, nil
}

// mergeBase is the best common ancestor of commits a and b, or "" when they share no history, which git merge-base
// answers by exiting 1 and printing nothing.
func (s *Server) mergeBase(ctx context.Context, dir, a, b string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := s.objectsGit(ctx, dir, "merge-base", a, b)
	cmd.Stdout, cmd.Stderr = &stdout, &limited{buf: &stderr, n: 4096}
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return strings.TrimSpace(stdout.String()), nil
	case errors.As(err, &exit) && exit.ExitCode() == 1 && stdout.Len() == 0 && stderr.Len() == 0:
		return "", nil
	}
	return "", fmt.Errorf("running git merge-base: %w: %s", err, strings.TrimSpace(stderr.String()))
}

// emptyTree is the name of the empty tree in the repository's object format, which Git knows without storing it.
func (s *Server) emptyTree(ctx context.Context, dir string) (string, error) {
	out, err := s.read(ctx, dir, strings.NewReader(""), "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// diffFiles lists the files that change from commit or tree from to commit to, in Git's order, with their statuses,
// paths, modes and counts, from git diff's raw and numstat outputs, which list the same files in the same order.
func (s *Server) diffFiles(ctx context.Context, dir, from, to string) ([]code.FileDiff, error) {
	raw, err := s.read(ctx, dir, nil, append(append([]string{"diff", "--raw", "-z"}, diffArgs...), from, to)...)
	if err != nil {
		return nil, err
	}
	files, err := parseRaw(raw)
	if err != nil {
		return nil, err
	}
	numstat, err := s.read(ctx, dir, nil, append(append([]string{"diff", "--numstat", "-z"}, diffArgs...), from, to)...)
	if err != nil {
		return nil, err
	}
	if err := addCounts(files, numstat); err != nil {
		return nil, err
	}
	return files, nil
}

// parseRaw reads git diff --raw -z's records, each
// ":<old mode> <new mode> <old object> <new object> <status>\0<path>\0", with the old path before the new one for a
// rename.
func parseRaw(out []byte) ([]code.FileDiff, error) {
	tokens := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	var files []code.FileDiff
	for i := 0; i < len(tokens) && tokens[i] != ""; i++ {
		fields := strings.Fields(strings.TrimPrefix(tokens[i], ":"))
		if !strings.HasPrefix(tokens[i], ":") || len(fields) != 5 || i+1 >= len(tokens) {
			return nil, fmt.Errorf("reading a diff: git diff --raw printed %q", tokens[i])
		}
		f := code.FileDiff{OldMode: mode(fields[0]), Mode: mode(fields[1]), Path: tokens[i+1]}
		i++
		switch fields[4][0] {
		case 'A':
			f.Status = code.FileAdded
		case 'D':
			f.Status = code.FileDeleted
		case 'T':
			f.Status = code.FileTypeChanged
		case 'R':
			if i+1 >= len(tokens) {
				return nil, fmt.Errorf("reading a diff: git diff --raw printed a rename without its new path")
			}
			f.Status, f.OldPath, f.Path = code.FileRenamed, f.Path, tokens[i+1]
			i++
		default:
			f.Status = code.FileModified
		}
		files = append(files, f)
	}
	return files, nil
}

// mode is a raw record's mode, "" for the zeros that stand for a side that has no file.
func mode(m string) string {
	if strings.Trim(m, "0") == "" {
		return ""
	}
	return m
}

// addCounts adds to files the counts of git diff --numstat -z's records, one per file in the same order, each
// "<added>\t<removed>\t<path>\0", or "<added>\t<removed>\t\0<old path>\0<new path>\0" for a rename; a binary file's
// counts are "-".
func addCounts(files []code.FileDiff, out []byte) error {
	tokens := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	n := 0
	for i := 0; i < len(tokens) && tokens[i] != ""; i++ {
		fields := strings.SplitN(tokens[i], "\t", 3)
		if len(fields) != 3 || n >= len(files) {
			return fmt.Errorf("reading a diff: git diff --numstat printed %q", tokens[i])
		}
		if fields[2] == "" {
			i += 2 // a rename's two paths, which the raw record gave
		}
		f := &files[n]
		n++
		if fields[0] == "-" {
			f.Binary = true
			continue
		}
		added, errAdded := strconv.Atoi(fields[0])
		removed, errRemoved := strconv.Atoi(fields[1])
		if errAdded != nil || errRemoved != nil {
			return fmt.Errorf("reading a diff: git diff --numstat printed %q", tokens[i])
		}
		f.Additions, f.Deletions = added, removed
	}
	if n != len(files) {
		return fmt.Errorf("reading a diff: git diff --numstat listed %d files, and --raw %d", n, len(files))
	}
	return nil
}

// diffHunks adds to files, which git diff lists from from to to in this order, their hunks, read from its patch as it
// prints it, up to the limits of a diff: past them, the files keep their counts but not their hunks. Git prints a
// path whose kind changed as two patches, its deletion and its creation, whose hunks it joins.
func (s *Server) diffHunks(ctx context.Context, dir, from, to string, files []code.FileDiff) ([]code.FileDiff, error) {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	var stderr bytes.Buffer
	cmd := s.objectsGit(ctx, dir, append(append([]string{"diff", "--patch"}, diffArgs...), from, to)...)
	cmd.Stderr = &limited{buf: &stderr, n: 4096}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("running git diff: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("running git diff: %w", err)
	}
	p := &patch{files: files}
	readErr := p.read(bufio.NewReaderSize(out, code.MaxDiffLineBytes+2)) // a line's text, its prefix and its newline
	if p.done {
		stop() // what is left nobody reads; a patch read to its end ends on its own
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, fmt.Errorf("reading git diff's patch: %w", readErr)
	}
	if waitErr != nil && !p.done {
		return nil, fmt.Errorf("running git diff: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	p.leaveOut()
	return p.files, nil
}

// patch reads git diff's patch into its files' hunks: the file it reads, how many of its patches remain, how many lines
// it and all the files before it hold, and whether the rest is left out.
type patch struct {
	files   []code.FileDiff
	current int
	// patches is how many of the current file's patches are left, two for a path whose kind changed.
	patches int
	lines   int
	total   int
	hunk    *code.Hunk
	old     int
	new     int
	// skip drops the current file's hunks, once it passed a limit.
	skip bool
	// done stops the reading, once the files listed are read or the diff passed MaxDiffLines.
	done bool
}

// read reads the patch line by line until its end, or until done.
func (p *patch) read(r *bufio.Reader) error {
	p.current = -1
	for !p.done {
		line, err := r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			p.drop()
			if err = discardLine(r); err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			continue
		}
		if len(line) > 0 {
			p.line(strings.TrimSuffix(string(line), "\n"))
		}
		if errors.Is(err, io.EOF) {
			p.endFile()
			return nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// discardLine reads what is left of a line too long to keep.
func discardLine(r *bufio.Reader) error {
	for {
		_, err := r.ReadSlice('\n')
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
		case err != nil:
			return fmt.Errorf("discarding a long line: %w", err)
		default:
			return nil
		}
	}
}

// line reads one line of the patch: a patch's first line, a hunk's header or one of its lines, or a header line, which
// says nothing the raw and numstat outputs didn't.
func (p *patch) line(line string) {
	switch {
	case strings.HasPrefix(line, "diff --git "):
		p.startPatch()
	case p.current < 0 || p.skip:
	case strings.HasPrefix(line, "@@ "):
		p.startHunk(line)
	case p.hunk != nil:
		p.hunkLine(line)
	}
}

// startPatch starts the next patch: the current file's second, for a path whose kind changed, or the next file's.
func (p *patch) startPatch() {
	p.hunk = nil
	if p.patches > 0 {
		p.patches--
		return
	}
	p.endFile()
	if p.done {
		return
	}
	p.current++
	if p.current >= len(p.files) {
		p.done = true
		return
	}
	p.patches, p.lines, p.skip = 0, 0, false
	if p.files[p.current].Status == code.FileTypeChanged {
		p.patches = 1
	}
}

// startHunk starts a hunk at its header, "@@ -<old start>[,<old lines>] +<new start>[,<new lines>] @@ <section>".
func (p *patch) startHunk(line string) {
	header, section, _ := strings.Cut(strings.TrimPrefix(line, "@@ "), " @@")
	oldRange, newRange, _ := strings.Cut(header, " ")
	h := code.Hunk{Section: strings.TrimSpace(section)}
	h.OldStart, h.OldLines = hunkRange(strings.TrimPrefix(oldRange, "-"))
	h.NewStart, h.NewLines = hunkRange(strings.TrimPrefix(newRange, "+"))
	f := &p.files[p.current]
	f.Hunks = append(f.Hunks, h)
	p.hunk = &f.Hunks[len(f.Hunks)-1]
	p.old, p.new = h.OldStart, h.NewStart
}

// hunkRange reads "<start>[,<lines>]", whose lines are 1 when it doesn't say.
func hunkRange(r string) (int, int) {
	start, count, ok := strings.Cut(r, ",")
	s, _ := strconv.Atoi(start) // Git writes numbers; anything else reads as 0
	if !ok {
		return s, 1
	}
	n, _ := strconv.Atoi(count) // as above
	return s, n
}

// hunkLine reads one of a hunk's lines: context, added, removed, or Git's note that the line before it ends without a
// newline. An empty line is context whose blank Git left out.
func (p *patch) hunkLine(line string) {
	if strings.HasPrefix(line, `\`) {
		if n := len(p.hunk.Lines); n > 0 {
			p.hunk.Lines[n-1].NoNewline = true
		}
		return
	}
	l := code.DiffLine{Kind: code.LineContext}
	if line != "" {
		l.Text = line[1:]
		switch line[0] {
		case '+':
			l.Kind = code.LineAdded
		case '-':
			l.Kind = code.LineRemoved
		}
	}
	switch l.Kind {
	case code.LineContext:
		l.Old, l.New = p.old, p.new
		p.old++
		p.new++
	case code.LineAdded:
		l.New = p.new
		p.new++
	case code.LineRemoved:
		l.Old = p.old
		p.old++
	}
	p.hunk.Lines = append(p.hunk.Lines, l)
	if p.lines++; p.lines > code.MaxFileDiffLines {
		p.drop()
	}
}

// drop leaves out the current file's hunks, which passed a limit.
func (p *patch) drop() {
	if p.current < 0 || p.current >= len(p.files) {
		return
	}
	p.files[p.current].Hunks, p.files[p.current].Truncated = nil, true
	p.hunk, p.skip = nil, true
}

// endFile counts the current file's lines towards the diff's, and stops the reading once they pass MaxDiffLines.
func (p *patch) endFile() {
	if p.current < 0 || p.current >= len(p.files) || p.skip {
		return
	}
	if p.total+p.lines > code.MaxDiffLines {
		p.drop()
		p.done = true
		return
	}
	p.total += p.lines
}

// leaveOut marks the files the reading never reached, once it stopped early, as left out, but those with no lines to
// show: binary files, and those whose change is their mode alone.
func (p *patch) leaveOut() {
	if !p.done {
		return
	}
	for i := max(p.current+1, 0); i < len(p.files); i++ {
		f := &p.files[i]
		f.Truncated = !f.Binary && f.Additions+f.Deletions > 0
	}
}
