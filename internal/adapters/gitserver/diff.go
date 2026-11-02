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
// configuration that changes what it prints. The patch's context lines are its own option: --unified implies --patch,
// which would follow the raw and numstat listings with the whole patch.
var diffArgs = []string{"--find-renames", "--no-color", "--no-ext-diff", "--no-textconv"}

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
	files, totals, err := s.diffFiles(ctx, o.dir, from, o.commit)
	if err != nil {
		return err
	}
	d.Additions, d.Deletions, d.FilesLeftOut = totals.additions, totals.deletions, totals.files-len(files)
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

// diffTotals counts every file a diff changes, and the lines added to and removed from them, listed or not.
type diffTotals struct {
	files, additions, deletions int
}

// diffFiles lists the first code.MaxDiffFiles of the files that change from commit or tree from to commit to, in Git's
// order, with their statuses, paths, modes and counts, and counts every file and line the diff changes. It reads git
// diff's raw and numstat outputs, which list the same files in the same order, as git prints them: the raw output only
// as far as the files listed, and the numstat output to its end, keeping its totals but no file past the limit, so a
// diff of any size costs the same memory.
func (s *Server) diffFiles(ctx context.Context, dir, from, to string) ([]code.FileDiff, diffTotals, error) {
	var files []code.FileDiff
	var limited bool
	raw := append(append([]string{"diff", "--raw", "-z"}, diffArgs...), from, to)
	if err := s.stream(ctx, dir, func(r *bufio.Reader) (bool, error) {
		var err error
		files, limited, err = readRaw(&nulTokens{r: r}, code.MaxDiffFiles)
		return limited, err
	}, raw...); err != nil {
		return nil, diffTotals{}, err
	}
	var totals diffTotals
	numstat := append(append([]string{"diff", "--numstat", "-z"}, diffArgs...), from, to)
	if err := s.stream(ctx, dir, func(r *bufio.Reader) (bool, error) {
		var err error
		totals, err = addCounts(&nulTokens{r: r}, files, limited)
		return false, err
	}, numstat...); err != nil {
		return nil, diffTotals{}, err
	}
	return files, totals, nil
}

// stream runs git with args over the repository at dir and hands consume its output as git prints it. When consume
// stops before the end, or fails, git is stopped too, and how it exits is no longer an error.
func (s *Server) stream(ctx context.Context, dir string, consume func(*bufio.Reader) (stopped bool, err error), args ...string) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	var stderr bytes.Buffer
	cmd := s.objectsGit(ctx, dir, args...)
	cmd.Stderr = &limited{buf: &stderr, n: 4096}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("running git %s: %w", args[0], err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("running git %s: %w", args[0], err)
	}
	stopped, readErr := consume(bufio.NewReaderSize(out, code.MaxDiffLineBytes+2)) // a line's text, its prefix and its newline
	if stopped || readErr != nil {
		stop() // what is left nobody reads; output read to its end ends on its own
	}
	waitErr := cmd.Wait()
	switch {
	case readErr != nil:
		return fmt.Errorf("reading git %s: %w", args[0], readErr)
	case waitErr != nil && !stopped:
		return fmt.Errorf("running git %s: %w: %s", args[0], waitErr, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// maxDiffToken is the longest path git diff's -z output may name, past which the diff fails rather than hold it.
const maxDiffToken = 1 << 20

// nulTokens reads the NUL-terminated tokens of git's -z output one at a time.
type nulTokens struct {
	r   *bufio.Reader
	buf []byte
}

// next reads the next token, or answers false at the end of the output.
func (t *nulTokens) next() (string, bool, error) {
	t.buf = t.buf[:0]
	for {
		chunk, err := t.r.ReadSlice(0)
		t.buf = append(t.buf, chunk...)
		switch {
		case err == nil:
			return string(t.buf[:len(t.buf)-1]), true, nil
		case errors.Is(err, bufio.ErrBufferFull) && len(t.buf) <= maxDiffToken:
		case errors.Is(err, bufio.ErrBufferFull):
			return "", false, fmt.Errorf("a token longer than %d bytes", maxDiffToken)
		case errors.Is(err, io.EOF) && len(t.buf) == 0:
			return "", false, nil
		case errors.Is(err, io.EOF):
			return "", false, fmt.Errorf("the output ended inside a token: %w", io.ErrUnexpectedEOF)
		default:
			return "", false, fmt.Errorf("reading a token: %w", err)
		}
	}
}

// must reads the next token, which a record needs.
func (t *nulTokens) must(record string) (string, error) {
	token, ok, err := t.next()
	switch {
	case err != nil:
		return "", err
	case !ok:
		return "", fmt.Errorf("the output ended inside record %q: %w", record, io.ErrUnexpectedEOF)
	}
	return token, nil
}

// readRaw reads at most limit of git diff --raw -z's records, each
// ":<old mode> <new mode> <old object> <new object> <status>\0<path>\0", with the old path before the new one for a
// rename, and answers whether it stopped at the limit, short of any records that may follow.
func readRaw(t *nulTokens, limit int) ([]code.FileDiff, bool, error) {
	var files []code.FileDiff
	for len(files) < limit {
		record, ok, err := t.next()
		if err != nil || !ok {
			return files, false, err
		}
		fields := strings.Fields(strings.TrimPrefix(record, ":"))
		if !strings.HasPrefix(record, ":") || len(fields) != 5 || fields[4] == "" {
			return nil, false, fmt.Errorf("git diff --raw printed %q", record)
		}
		f := code.FileDiff{OldMode: mode(fields[0]), Mode: mode(fields[1])}
		if f.Path, err = t.must(record); err != nil {
			return nil, false, err
		}
		switch fields[4][0] {
		case 'A':
			f.Status = code.FileAdded
		case 'D':
			f.Status = code.FileDeleted
		case 'T':
			f.Status = code.FileTypeChanged
		case 'R':
			f.Status, f.OldPath = code.FileRenamed, f.Path
			if f.Path, err = t.must(record); err != nil {
				return nil, false, err
			}
		default:
			f.Status = code.FileModified
		}
		files = append(files, f)
	}
	return files, true, nil
}

// mode is a raw record's mode, "" for the zeros that stand for a side that has no file.
func mode(m string) string {
	if strings.Trim(m, "0") == "" {
		return ""
	}
	return m
}

// addCounts reads git diff --numstat -z's records to their end, one per file in the order --raw lists them, each
// "<added>\t<removed>\t<path>\0", or "<added>\t<removed>\t\0<old path>\0<new path>\0" for a rename, with "-" for a
// binary file's counts. It adds their counts to the files listed, which are the first of them, and counts every file
// and line; it lists more files than those only when the listing was limited.
func addCounts(t *nulTokens, files []code.FileDiff, limited bool) (diffTotals, error) {
	var totals diffTotals
	for {
		record, ok, err := t.next()
		if err != nil {
			return diffTotals{}, err
		}
		if !ok {
			break
		}
		f, err := numstatRecord(t, record)
		if err != nil {
			return diffTotals{}, err
		}
		if n := totals.files; n < len(files) {
			files[n].Additions, files[n].Deletions, files[n].Binary = f.Additions, f.Deletions, f.Binary
		}
		totals.files++
		totals.additions += f.Additions
		totals.deletions += f.Deletions
	}
	if totals.files < len(files) || (totals.files > len(files) && !limited) {
		return diffTotals{}, fmt.Errorf("git diff --numstat listed %d files, and --raw %d", totals.files, len(files))
	}
	return totals, nil
}

// numstatRecord reads the counts of one of git diff --numstat -z's records, reading past a rename's two paths, which
// the raw record gave.
func numstatRecord(t *nulTokens, record string) (code.FileDiff, error) {
	fields := strings.SplitN(record, "\t", 3)
	if len(fields) != 3 {
		return code.FileDiff{}, fmt.Errorf("git diff --numstat printed %q", record)
	}
	if fields[2] == "" {
		for range 2 {
			if _, err := t.must(record); err != nil {
				return code.FileDiff{}, err
			}
		}
	}
	if fields[0] == "-" {
		return code.FileDiff{Binary: true}, nil
	}
	added, errAdded := strconv.Atoi(fields[0])
	removed, errRemoved := strconv.Atoi(fields[1])
	if errAdded != nil || errRemoved != nil {
		return code.FileDiff{}, fmt.Errorf("git diff --numstat printed %q", record)
	}
	return code.FileDiff{Additions: added, Deletions: removed}, nil
}

// diffHunks adds to files, which git diff lists from from to to in this order, their hunks, read from its patch as it
// prints it, up to the limits of a diff: past them, the files keep their counts but not their hunks. Git prints a
// path whose kind changed as two patches, its deletion and its creation, whose hunks it joins.
func (s *Server) diffHunks(ctx context.Context, dir, from, to string, files []code.FileDiff) ([]code.FileDiff, error) {
	p := &patch{files: files}
	if err := s.stream(ctx, dir, func(r *bufio.Reader) (bool, error) {
		err := p.read(r)
		return p.done, err
	}, append(append([]string{"diff", "--patch", "--unified=3"}, diffArgs...), from, to)...); err != nil {
		return nil, err
	}
	p.leaveOut()
	return p.files, nil
}

// patch reads git diff's patch into its files' hunks: the file it reads, how many of its patches remain, how many lines
// and how much text it and all the files before it hold, and whether the rest is left out.
type patch struct {
	files   []code.FileDiff
	current int
	// patches is how many of the current file's patches are left, two for a path whose kind changed.
	patches int
	lines   int
	total   int
	// text is how many bytes of text the current file's hunk lines hold, and totalText the files' before it.
	text      int
	totalText int
	hunk      *code.Hunk
	old       int
	new       int
	// skip drops the current file's hunks, once it passed a limit.
	skip bool
	// done stops the reading, once the files listed are read, the diff passed MaxDiffLines or MaxDiffBytes, or the
	// reading skipped maxSkippedBytes.
	done bool
	// skipped is how many bytes of the patch the reading went through without keeping them.
	skipped int
}

// maxSkippedBytes bounds how much of the patch the reading goes through past the hunks it leaves out, looking for the
// next file's: past it, the reading stops and the files after are left out, as past code.MaxDiffBytes, so one file
// too large to show doesn't have the Git server read all of it.
const maxSkippedBytes = 4 << 20

// patchHeader starts each patch.
var patchHeader = []byte("diff --git ")

// read reads the patch line by line until its end, or until done. It reads a file's lines it leaves out without
// keeping them, a line too long to keep a buffer at a time, and stops once it skipped more than maxSkippedBytes.
func (p *patch) read(r *bufio.Reader) error {
	p.current = -1
	inLong := false // the line read continues one too long to keep
	for !p.done {
		line, err := r.ReadSlice('\n')
		long := errors.Is(err, bufio.ErrBufferFull)
		switch {
		case long || inLong:
			p.drop()
			p.skipped += len(line)
		case p.skip && !bytes.HasPrefix(line, patchHeader):
			p.skipped += len(line)
		case len(line) > 0:
			p.line(strings.TrimSuffix(string(line), "\n"))
		}
		inLong = long
		if p.skipped > maxSkippedBytes {
			p.done = true
		}
		switch {
		case errors.Is(err, io.EOF):
			p.endFile()
			return nil
		case err != nil && !long:
			return fmt.Errorf("reading git diff's patch: %w", err)
		}
	}
	return nil
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
	p.patches, p.lines, p.text, p.skip = 0, 0, 0, false
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
	p.lines++
	p.text += len(l.Text)
	switch {
	case p.lines > code.MaxFileDiffLines || p.text > code.MaxFileDiffBytes:
		p.drop()
	case p.total+p.lines > code.MaxDiffLines || p.totalText+p.text > code.MaxDiffBytes:
		// The diff can't hold this file, and holds nothing after it: the reading stops before it holds more.
		p.drop()
		p.done = true
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

// endFile counts the current file's lines and text towards the diff's, once it kept them.
func (p *patch) endFile() {
	if p.current < 0 || p.current >= len(p.files) || p.skip {
		return
	}
	p.total += p.lines
	p.totalText += p.text
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
