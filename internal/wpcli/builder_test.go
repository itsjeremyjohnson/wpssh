package wpcli

import (
	"testing"
)

func TestNew(t *testing.T) {
	cmd := New("plugin", "list")
	if cmd == nil {
		t.Fatal("New returned nil")
	}
	if len(cmd.parts) != 2 || cmd.parts[0] != "plugin" || cmd.parts[1] != "list" {
		t.Errorf("parts = %v, want [plugin list]", cmd.parts)
	}
}

func TestBuildBasic(t *testing.T) {
	got := New("plugin", "list").Build("/var/www/html")
	want := "cd '/var/www/html' && wp plugin list"
	if got != want {
		t.Errorf("Build() = %q, want %q", got, want)
	}
}

func TestBuildWithFormat(t *testing.T) {
	got := New("plugin", "list").Format("json").Build("/var/www/html")
	want := "cd '/var/www/html' && wp plugin list --format='json'"
	if got != want {
		t.Errorf("Build() = %q, want %q", got, want)
	}
}

func TestBuildWithFlag(t *testing.T) {
	got := New("plugin", "list").Format("json").Flag("status", "active").Build("/var/www/html")
	want := "cd '/var/www/html' && wp plugin list --format='json' --status='active'"
	if got != want {
		t.Errorf("Build() = %q, want %q", got, want)
	}
}

func TestBuildWithBoolFlag(t *testing.T) {
	got := New("plugin", "update").BoolFlag("all").Build("/var/www/html")
	want := "cd '/var/www/html' && wp plugin update --all"
	if got != want {
		t.Errorf("Build() = %q, want %q", got, want)
	}
}

func TestBuildWithArg(t *testing.T) {
	got := New("plugin", "install").Arg("akismet").BoolFlag("activate").Build("/var/www/html")
	want := "cd '/var/www/html' && wp plugin install 'akismet' --activate"
	if got != want {
		t.Errorf("Build() = %q, want %q", got, want)
	}
}

func TestBuildFlagsSorted(t *testing.T) {
	got := New("plugin", "list").Flag("status", "active").Flag("field", "name").Build("/var/www/html")
	want := "cd '/var/www/html' && wp plugin list --field='name' --status='active'"
	if got != want {
		t.Errorf("Build() flags not sorted: %q, want %q", got, want)
	}
}

func TestShellEscapeSingleQuotes(t *testing.T) {
	got := New("eval").Arg("echo 'hello';").Build("/var/www/html")
	want := "cd '/var/www/html' && wp eval 'echo '\\''hello'\\'';"
	// The value "echo 'hello';" should become 'echo '\''hello'\'';"
	wantEscaped := `cd '/var/www/html' && wp eval 'echo '\''hello'\''` + `;'`
	if got != want && got != wantEscaped {
		t.Errorf("Build() with quotes = %q", got)
	}
}

func TestShellEscapePath(t *testing.T) {
	got := New("plugin", "list").Build("/home/user/my site")
	want := "cd '/home/user/my site' && wp plugin list"
	if got != want {
		t.Errorf("Build() path with space = %q, want %q", got, want)
	}
}

func TestShellEscapeSpecialChars(t *testing.T) {
	// Test that shell metacharacters are safely escaped
	got := New("option", "update").Arg("blogname").Arg("My $ite & Blog").Build("/var/www")
	want := "cd '/var/www' && wp option update 'blogname' 'My $ite & Blog'"
	if got != want {
		t.Errorf("Build() special chars = %q, want %q", got, want)
	}
}

func TestCacheKeyBasic(t *testing.T) {
	key := New("plugin", "list").Format("json").CacheKey()
	want := "plugin list:--format=json"
	if key != want {
		t.Errorf("CacheKey() = %q, want %q", key, want)
	}
}

func TestCacheKeyWithFlags(t *testing.T) {
	key := New("plugin", "list").Format("json").Flag("status", "active").CacheKey()
	want := "plugin list:--format=json --status=active"
	if key != want {
		t.Errorf("CacheKey() = %q, want %q", key, want)
	}
}

func TestCacheKeyDeterministic(t *testing.T) {
	// Adding flags in different order should produce the same cache key
	key1 := New("plugin", "list").Flag("status", "active").Flag("field", "name").Format("json").CacheKey()
	key2 := New("plugin", "list").Flag("field", "name").Flag("status", "active").Format("json").CacheKey()
	if key1 != key2 {
		t.Errorf("CacheKey() not deterministic: %q != %q", key1, key2)
	}
}

func TestCacheKeyNoFlags(t *testing.T) {
	key := New("core", "version").CacheKey()
	want := "core version"
	if key != want {
		t.Errorf("CacheKey() = %q, want %q", key, want)
	}
}

func TestCacheKeyBoolFlags(t *testing.T) {
	key := New("plugin", "update").BoolFlag("all").CacheKey()
	want := "plugin update:--all"
	if key != want {
		t.Errorf("CacheKey() = %q, want %q", key, want)
	}
}

func TestShellEscapeFunction(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "'hello'"},
		{"hello world", "'hello world'"},
		{"it's", `'it'\''s'`},
		{"", "''"},
		{"$HOME", "'$HOME'"},
		{"`whoami`", "'`whoami`'"},
		{"a;rm -rf /", "'a;rm -rf /'"},
	}

	for _, tt := range tests {
		got := shellEscape(tt.input)
		if got != tt.want {
			t.Errorf("shellEscape(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestPathEscape(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"~/public_html", "\"$HOME\"/'public_html'"},
		{"~", "\"$HOME\""},
		{"~/", "\"$HOME\""},
		{"~/sites/foo", "\"$HOME\"/'sites/foo'"},
		{"/var/www/html", "'/var/www/html'"},
		{"/home/user/my site", "'/home/user/my site'"},
	}

	for _, tt := range tests {
		got := pathEscape(tt.input)
		if got != tt.want {
			t.Errorf("pathEscape(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildTildePath(t *testing.T) {
	got := New("core", "version").Build("~/public_html")
	want := "cd \"$HOME\"/'public_html' && wp core version"
	if got != want {
		t.Errorf("Build(tilde) = %q, want %q", got, want)
	}
}
