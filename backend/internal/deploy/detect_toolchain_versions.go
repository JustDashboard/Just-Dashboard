package deploy

import (
	"regexp"
	"strconv"
	"strings"
)

// Version managers pin the JDK and the .NET SDK a repository is developed
// with in files of their own: .java-version (jenv), .sdkmanrc (SDKMAN!),
// .tool-versions (asdf, mise), mise.toml, and Heroku's system.properties.
// They are read as data, the nearest one to the project first.

// toolchainPin is one version a file pins, and the file that pins it.
type toolchainPin struct {
	version string
	source  string
}

var (
	// javaVersionTokenRE finds the numbers in the ways a Java version is
	// written: 21, 21.0.2, 1.8, 8u402, temurin-21.0.2+13, 21.0.2-tem.
	javaVersionTokenRE  = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)*`)
	dotnetSDKVersionRE  = regexp.MustCompile(`^([0-9]{1,2})\.([0-9]{1,2})(?:\.([0-9]{3,5}))?(-[0-9A-Za-z][0-9A-Za-z.-]{0,40})?$`)
	systemPropertyJavaR = regexp.MustCompile(`(?m)^\s*java\.runtime\.version\s*[=:]\s*(\S+)`)
)

// javaRelease is the Java release a version string names, or 0.
func javaRelease(value string) int {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return 0
	}
	// A vendor prefix (temurin-, openjdk-, zulu-) comes first; the release
	// is the first number after it that can be one, and 1.8 is Java 8.
	for _, token := range javaVersionTokenRE.FindAllString(value, 4) {
		major, rest, _ := strings.Cut(token, ".")
		if major == "1" && rest != "" {
			major, _, _ = strings.Cut(rest, ".")
		}
		if release, err := strconv.Atoi(major); err == nil && release >= 5 && release <= 40 {
			return release
		}
	}
	return 0
}

// javaVersionPin reads the JDK a directory's version files pin, in the
// order a developer's tools would consult them.
func javaVersionPin(files *buildFiles, dir string) (toolchainPin, int) {
	if content, ok := files.read(joinRootDir(dir, ".java-version"), 4096); ok {
		if release := javaRelease(firstMeaningfulLine(string(content))); release > 0 {
			return toolchainPin{version: strconv.Itoa(release), source: joinRootDir(dir, ".java-version")}, release
		}
	}
	if content, ok := files.read(joinRootDir(dir, ".sdkmanrc"), 4096); ok {
		for _, line := range strings.Split(string(content), "\n") {
			key, value, found := strings.Cut(strings.TrimSpace(line), "=")
			if found && strings.TrimSpace(key) == "java" {
				if release := javaRelease(value); release > 0 {
					return toolchainPin{version: strconv.Itoa(release), source: joinRootDir(dir, ".sdkmanrc")}, release
				}
			}
		}
	}
	if value := dirToolVersion(files, dir, "java"); value != "" {
		if release := javaRelease(value); release > 0 {
			return toolchainPin{version: strconv.Itoa(release), source: joinRootDir(dir, ".tool-versions")}, release
		}
	}
	if value, source := miseTool(files, dir, "java"); value != "" {
		if release := javaRelease(value); release > 0 {
			return toolchainPin{version: strconv.Itoa(release), source: source}, release
		}
	}
	if content, ok := files.read(joinRootDir(dir, "system.properties"), 4096); ok {
		if match := systemPropertyJavaR.FindStringSubmatch(string(content)); match != nil {
			if release := javaRelease(match[1]); release > 0 {
				return toolchainPin{version: strconv.Itoa(release), source: joinRootDir(dir, "system.properties")}, release
			}
		}
	}
	return toolchainPin{}, 0
}

// dotnetVersionPin reads the .NET SDK a version manager pins: asdf's
// dotnet or dotnet-core plugin, or mise's dotnet tool.
func dotnetVersionPin(files *buildFiles, dir string) toolchainPin {
	for _, tool := range []string{"dotnet", "dotnet-core"} {
		if value := dirToolVersion(files, dir, tool); value != "" && dotnetSDKVersionRE.MatchString(value) {
			return toolchainPin{version: value, source: joinRootDir(dir, ".tool-versions")}
		}
	}
	if value, source := miseTool(files, dir, "dotnet"); value != "" && dotnetSDKVersionRE.MatchString(value) {
		return toolchainPin{version: value, source: source}
	}
	return toolchainPin{}
}

// dirToolVersion is the first version the .tool-versions in dir lists for
// a tool, read the way the language recipes read theirs (toolVersionsEntry).
func dirToolVersion(files *buildFiles, dir, tool string) string {
	content, ok := files.read(joinRootDir(dir, ".tool-versions"), 16<<10)
	if !ok {
		return ""
	}
	return boundedSpec(toolVersionsEntry(content, tool))
}

// miseTool is the version mise's configuration gives a tool under [tools]:
// a string, the first of a list, or an inline table's version.
func miseTool(files *buildFiles, dir, tool string) (string, string) {
	for _, name := range []string{"mise.toml", ".mise.toml", ".config/mise.toml"} {
		content, ok := files.read(joinRootDir(dir, name), 64<<10)
		if !ok {
			continue
		}
		for _, entry := range readTOML(content) {
			if entry.table != "tools" {
				continue
			}
			switch entry.key {
			case tool, tool + ".version":
				value := entry.value.text
				if entry.value.isList && len(entry.value.list) > 0 {
					value = entry.value.list[0]
				}
				if value = boundedSpec(value); value != "" {
					return value, joinRootDir(dir, name)
				}
			}
		}
	}
	return "", ""
}

// joinRootDir joins a name onto a reader directory ("." for the top).
func joinRootDir(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return dir + "/" + name
}
