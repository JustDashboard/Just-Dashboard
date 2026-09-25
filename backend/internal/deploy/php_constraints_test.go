package deploy

import "testing"

// Composer's own semantics, from its documentation and VersionParser: the
// locked version either satisfies composer.json or `composer install` stops.
func TestComposerConstraintAllows(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		constraint, version string
		allowed, ok         bool
	}{
		{"^1.2", "1.9.0", true, true}, {"^1.2", "2.0.0", false, true}, {"^1.2", "1.1.9", false, true},
		{"^1.2", "v2.0.0-beta1", false, true}, {"^0.3", "0.3.9", true, true}, {"^0.3", "0.4.0", false, true},
		{"^0.0.3", "0.0.3", true, true}, {"^0.0.3", "0.0.4", false, true}, {"^8", "8.4.1", true, true},
		{"~1.2", "1.9.0", true, true}, {"~1.2", "2.0.0", false, true}, {"~1.2.3", "1.2.9", true, true},
		{"~1.2.3", "1.3.0", false, true}, {">=1.0 <2.0", "1.5", true, true}, {">=1.0,<2.0", "2.0", false, true},
		{">= 8.2", "8.2.0", true, true}, {"1.0.*", "1.0.7", true, true}, {"1.0.*", "1.1.0", false, true},
		{"8.*", "8.4.3", true, true}, {"*", "0.0.1", true, true}, {"^1.0 || ^2.0", "2.3.0", true, true},
		{"^1.0|^2.0", "3.0.0", false, true}, {"1.2.3", "v1.2.3", true, true}, {"1.2.3", "1.2.4", false, true},
		{"!=1.2.3", "1.2.4", true, true}, {"1.0 - 2.0", "2.0.5", true, true}, {"1.0 - 2.0", "2.1.0", false, true},
		{"1.0 - 2.0.1", "2.0.2", false, true}, {"^2.0@beta", "2.1.0-beta2", true, true},
		{">=2.0", "2.0.0-RC1", true, true}, {"<2.0", "2.0.0-RC1", false, true}, {"^3.0", "3.7.0.0", true, true},
		// Not verdicts: branches, aliases and hashes are left alone.
		{"dev-main", "dev-main", false, false}, {"^1.0", "dev-main", false, false},
		{"1.0.x-dev as 1.0.0", "1.0.0", false, false}, {"^1.0", "1.x-dev", false, false},
	} {
		allowed, ok := composerConstraintAllows(fixture.constraint, fixture.version)
		if allowed != fixture.allowed || ok != fixture.ok {
			t.Errorf("%q allows %q = %v, %v; want %v, %v", fixture.constraint, fixture.version, allowed, ok, fixture.allowed, fixture.ok)
		}
	}
	for _, name := range []string{"php", "php-64bit", "ext-intl", "lib-pcre", "composer-plugin-api", "composer-runtime-api", "composer"} {
		if !composerPlatformPackage(name) {
			t.Errorf("%s is a platform package", name)
		}
	}
	if composerPlatformPackage("laravel/framework") || composerPlatformPackage("phpunit/phpunit") {
		t.Error("a package was read as a platform requirement")
	}
}
