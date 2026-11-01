package gitserver

import "testing"

func TestSSHCommand(t *testing.T) {
	tests := []struct {
		name                       string
		command                    string
		wantService, wantOrg, want string
	}{
		{name: "a fetch from an ssh:// URL", command: "git-upload-pack '/acme/website.git'", wantService: uploadPack, wantOrg: "acme", want: "website"},
		{name: "a push from an scp-like URL", command: "git-receive-pack 'acme/website.git'", wantService: receivePack, wantOrg: "acme", want: "website"},
		{name: "a path without .git", command: "git-upload-pack '/acme/website'", wantService: uploadPack, wantOrg: "acme", want: "website"},
		{name: "an archive", command: "git-upload-archive '/acme/website.git'"},
		{name: "a shell's command", command: "ls -la"},
		{name: "an unquoted path", command: "git-upload-pack /acme/website.git"},
		{name: "a path left open", command: "git-upload-pack '/acme/website.git"},
		{name: "a quote within", command: `git-upload-pack '/acme/web'\''site.git'`},
		{name: "a deeper path", command: "git-upload-pack '/acme/website/more.git'"},
		{name: "no organization", command: "git-upload-pack '/website.git'"},
		{name: "an empty slug", command: "git-upload-pack '/acme/.git'"},
		{name: "nothing", command: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, org, slug, ok := sshCommand(tt.command)
			if ok != (tt.wantService != "") || service != tt.wantService || org != tt.wantOrg || slug != tt.want {
				t.Errorf("sshCommand(%q) = %q, %q, %q, %v, want %q, %q, %q", tt.command, service, org, slug, ok, tt.wantService, tt.wantOrg, tt.want)
			}
		})
	}
}
