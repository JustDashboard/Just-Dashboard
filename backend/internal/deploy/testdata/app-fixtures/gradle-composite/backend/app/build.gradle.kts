plugins {
    application
}

dependencies {
    implementation("jd:shared:1.0")
}

java {
    toolchain {
        languageVersion = JavaLanguageVersion.of(21)
    }
}

application {
    mainClass = "jd.app.App"
}
