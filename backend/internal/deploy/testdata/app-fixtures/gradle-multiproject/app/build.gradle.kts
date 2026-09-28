plugins {
    application
}

dependencies {
    implementation(project(":greeting"))
}

java {
    toolchain {
        languageVersion = JavaLanguageVersion.of(21)
    }
}

application {
    mainClass = "jd.app.App"
}
