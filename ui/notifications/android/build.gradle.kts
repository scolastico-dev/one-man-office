plugins {
    id("com.android.library")
    id("org.jetbrains.kotlin.android")
}
android {
    namespace = "dev.scolastico.omo.notifications"
    compileSdk = 36
    defaultConfig {
        minSdk = 24
        consumerProguardFiles("consumer-rules.pro")
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_1_8
        targetCompatibility = JavaVersion.VERSION_1_8
    }
    kotlinOptions { jvmTarget = "1.8" }
    testOptions { unitTests.isIncludeAndroidResources = true }
}
dependencies {
    implementation("androidx.webkit:webkit:1.14.0")
    implementation("androidx.activity:activity-ktx:1.10.1")
    implementation(project(":tauri-android"))
    implementation("androidx.work:work-runtime-ktx:2.11.0")
    implementation("androidx.core:core-ktx:1.17.0")
    testImplementation("androidx.room:room-runtime:2.8.4")
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.robolectric:robolectric:4.16.1")
    testImplementation("androidx.work:work-testing:2.11.0")
}

// Robolectric would download the Android jar of each tested SDK from Maven while
// the tests run, so a test outcome would depend on the network. Gradle resolves
// the jars instead, checks and caches them like any dependency, and Robolectric
// runs offline against that folder: a missing jar fails every run the same way.
// Each SDK named by a test's @Config needs its entry here
// (ui/tests/release.test.mjs checks this).
// One configuration per jar: the jars are versions of one module, and a single
// configuration would keep only the newest.
val robolectricSdks = mapOf(
    "27" to "8.1.0-robolectric-4611349-i7",
    "35" to "15-robolectric-13954326-i7",
).map { (sdk, version) ->
    configurations.create("robolectricSdk$sdk").also {
        dependencies.add(it.name, "org.robolectric:android-all-instrumented:$version")
    }
}
val robolectricSdkDir = layout.buildDirectory.dir("robolectric-sdks")
val stageRobolectricSdks by tasks.registering(Sync::class) {
    robolectricSdks.forEach { from(it) }
    into(robolectricSdkDir)
}
tasks.withType<Test>().configureEach {
    dependsOn(stageRobolectricSdks)
    systemProperty("robolectric.offline", "true")
    systemProperty("robolectric.dependency.dir", robolectricSdkDir.get().asFile.absolutePath)
}
