pluginManagement {
    repositories {
        // Resolve Central dependencies before the plugin portal redirects there.
        maven { url = uri("https://maven-central.storage-download.googleapis.com/maven2/") }
        gradlePluginPortal()
    }
}
