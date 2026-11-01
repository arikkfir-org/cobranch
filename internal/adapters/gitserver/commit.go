package gitserver

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"cobranch.dev/internal/services/git"
)

// parseCommit reads a commit object's content, as git cat-file prints it, into the domain's commit: its tree,
// parents, author and committer with their times' zones, signature and message, whole.
func parseCommit(sha string, content []byte) (git.Commit, error) {
	header, message, _ := bytes.Cut(content, []byte("\n\n"))
	c := git.Commit{SHA: sha, Message: string(message), Parents: []string{}}
	var key, value string
	flush := func() error {
		if key == "" {
			return nil
		}
		if err := setHeader(&c, key, value); err != nil {
			return fmt.Errorf("commit %s: %w", sha, err)
		}
		return nil
	}
	for line := range strings.SplitSeq(string(header), "\n") {
		if rest, ok := strings.CutPrefix(line, " "); ok && key != "" {
			value += "\n" + rest // a continuation, as a signature's lines are
			continue
		}
		if err := flush(); err != nil {
			return git.Commit{}, err
		}
		key, value, _ = strings.Cut(line, " ")
	}
	if err := flush(); err != nil {
		return git.Commit{}, err
	}
	if c.Tree == "" {
		return git.Commit{}, fmt.Errorf("commit %s names no tree", sha)
	}
	return c, nil
}

// setHeader reads one of a commit's headers into c, ignoring those this slice keeps nothing of (encoding, mergetag).
func setHeader(c *git.Commit, key, value string) error {
	var err error
	switch key {
	case "tree":
		c.Tree = value
	case "parent":
		c.Parents = append(c.Parents, value)
	case "author":
		c.Author, err = parsePerson(value)
	case "committer":
		c.Committer, err = parsePerson(value)
	case "gpgsig", "gpgsig-sha256":
		c.Signature = value
	}
	if err != nil {
		return fmt.Errorf("reading its %s: %w", key, err)
	}
	return nil
}

// parsePerson reads an author or committer line: Name <email> seconds zone.
func parsePerson(value string) (git.Person, error) {
	open, end := strings.Index(value, "<"), strings.LastIndex(value, ">")
	if open < 0 || end < open {
		return git.Person{}, fmt.Errorf("%q is not a name and an email", value)
	}
	p := git.Person{Name: strings.TrimSpace(value[:open]), Email: value[open+1 : end]}
	seconds, zone, _ := strings.Cut(strings.TrimSpace(value[end+1:]), " ")
	unix, err := strconv.ParseInt(seconds, 10, 64)
	if err != nil {
		return git.Person{}, fmt.Errorf("%q is no time", seconds)
	}
	p.Zone, p.At = zone, time.Unix(unix, 0).In(location(zone))
	return p, nil
}

// location is the fixed zone Git writes as +hhmm or -hhmm, or UTC for anything else.
func location(zone string) *time.Location {
	if len(zone) != 5 || (zone[0] != '+' && zone[0] != '-') {
		return time.UTC
	}
	hours, err1 := strconv.Atoi(zone[1:3])
	minutes, err2 := strconv.Atoi(zone[3:5])
	if err1 != nil || err2 != nil {
		return time.UTC
	}
	offset := hours*3600 + minutes*60
	if zone[0] == '-' {
		offset = -offset
	}
	return time.FixedZone(zone, offset)
}

// readObjects reads git cat-file --batch's output, each object's header line and then its content, and calls fn
// with each.
func readObjects(out io.Reader, fn func(sha, kind string, content []byte) error) error {
	r := bufio.NewReader(out)
	for {
		line, err := r.ReadString('\n')
		if err == io.EOF && line == "" {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading an object's header: %w", err)
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return fmt.Errorf("git cat-file printed %q", strings.TrimSpace(line))
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return fmt.Errorf("git cat-file printed %q", strings.TrimSpace(line))
		}
		content := make([]byte, size+1) // and its newline
		if _, err := io.ReadFull(r, content); err != nil {
			return fmt.Errorf("reading object %s: %w", fields[0], err)
		}
		if err := fn(fields[0], fields[1], content[:size]); err != nil {
			return err
		}
	}
}
