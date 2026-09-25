package deploy

import (
	"reflect"
	"testing"
)

// JVM and .NET failures the recipes can still meet — a private repository
// that refuses the build, a toolchain JDK the image lacks, an Android module
// Gradle cannot configure — are named from the build's own output, with the
// setting that fixes them when one does.
func TestBuildFailureCauseNamesJVMAndDotnetFailures(t *testing.T) {
	t.Parallel()
	java := BuildPlanConfig{Method: BuildRecipe, Recipe: "java"}
	for _, test := range []buildCase{
		{
			name: "Maven repository refuses the credentials", command: "mvn -B -ntp -DskipTests package", exit: 1, build: java,
			lines: []string{"[ERROR] Failed to execute goal on project api: Could not resolve dependencies for project com.acme:api:jar:1.0: Could not transfer artifact com.acme:core:pom:1.2 from/to github (https://maven.pkg.github.com/acme/libs): status code: 401, reason phrase: Unauthorized (401) -> [Help 1]"},
			want:  BuildCause{Code: "build_registry_auth", Phase: phaseBuild, Command: "mvn -B -ntp -DskipTests package", ExitCode: 1, Detail: "java"},
		},
		{
			name: "Gradle repository refuses the credentials", command: "./gradlew --no-daemon --console=plain :app:bootJar", exit: 1, build: java,
			lines: []string{"   > Could not GET 'https://maven.pkg.github.com/acme/libs/com/acme/core/1.2/core-1.2.pom'. Received status code 401 from server: Unauthorized"},
			want:  BuildCause{Code: "build_registry_auth", Phase: phaseBuild, Command: "./gradlew --no-daemon --console=plain :app:bootJar", ExitCode: 1, Detail: "java"},
		},
		{
			name: "Gradle toolchain the image lacks", command: "./gradlew --no-daemon --console=plain :bootJar", exit: 1, build: java,
			lines: []string{"   > No matching toolchains found for requested specification: {languageVersion=17, vendor=any vendor, implementation=vendor-specific} for LINUX on x86_64."},
			want: BuildCause{Code: "build_runtime_version", Phase: phaseBuild, Command: "./gradlew --no-daemon --console=plain :bootJar", ExitCode: 1, Detail: "gradle-toolchain", Subjects: []string{"17"},
				Fix: &CauseFix{Kind: fixSetBuild, Field: "configuration.build.javaVersion", Value: "17"}},
		},
		{
			name: "javac older than the release", command: "mvn -B -ntp -DskipTests package", exit: 1, build: java,
			lines: []string{"[ERROR] Failed to execute goal org.apache.maven.plugins:maven-compiler-plugin:3.13.0:compile (default-compile) on project api: Fatal error compiling: error: release version 24 not supported -> [Help 1]"},
			want: BuildCause{Code: "build_runtime_version", Phase: phaseBuild, Command: "mvn -B -ntp -DskipTests package", ExitCode: 1, Detail: "java", Subjects: []string{"24"},
				Fix: &CauseFix{Kind: fixSetBuild, Field: "configuration.build.javaVersion", Value: "25"}},
		},
		{
			name: "Android SDK", command: "gradle --no-daemon --console=plain :server:buildFatJar", exit: 1, build: java,
			lines: []string{"SDK location not found. Define a valid SDK location with an ANDROID_HOME environment variable or by setting the sdk.dir path in your project's local properties file at '/src/local.properties'."},
			want:  BuildCause{Code: "build_system_library_missing", Phase: phaseBuild, Command: "gradle --no-daemon --console=plain :server:buildFatJar", ExitCode: 1, Detail: "android", Subjects: []string{"android-sdk"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			collector, err := feedBuild(test)
			cause := buildFailureCause(err, collector, causeContext{build: test.build, hostMemory: 4 << 30}, nil)
			if cause == nil {
				t.Fatal("no cause")
			}
			got := *cause
			got.LineSeq = 0
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("cause = %+v\nwant    %+v", got, test.want)
			}
			if sentence := cause.sentence(); sentence == "" || causeTitle(cause.Code) == "" {
				t.Fatalf("sentence %q title %q", sentence, causeTitle(cause.Code))
			}
		})
	}
}
