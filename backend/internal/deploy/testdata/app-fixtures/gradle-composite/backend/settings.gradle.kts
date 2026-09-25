rootProject.name = "jd-acceptance-composite"

// The shared library is its own build beside this one; Gradle substitutes
// it for the jd:shared dependency.
includeBuild("../shared")

include("app")
