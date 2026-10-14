plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.ksp)
    alias(libs.plugins.hilt)
}

// Release signing comes from the environment (CI) or gradle properties (local). Read lazily so the
// configuration cache tracks the inputs; an unset or unreadable keystore just means an unsigned release build.
fun releaseProp(name: String): Provider<String> =
    providers.environmentVariable(name).orElse(providers.gradleProperty(name))

val releaseKeystorePath = releaseProp("FETCHARR_KEYSTORE_PATH")
val releaseKeystorePassword = releaseProp("FETCHARR_KEYSTORE_PASSWORD")
val releaseKeyAlias = releaseProp("FETCHARR_KEY_ALIAS")
val releaseKeyPassword = releaseProp("FETCHARR_KEY_PASSWORD")
val releaseKeystoreFile: File? = releaseKeystorePath.orNull?.takeIf { it.isNotBlank() }?.let(::file)?.takeIf { it.isFile }
val canSignRelease = releaseKeystoreFile != null &&
    releaseKeystorePassword.isPresent && releaseKeyAlias.isPresent && releaseKeyPassword.isPresent

android {
    namespace = "com.istarvin.fetcharr"
    compileSdk = 37 // libs (compose 1.12, core-ktx 1.19, okhttp 5.5) require >= 37; targetSdk stays 36

    defaultConfig {
        applicationId = "com.istarvin.fetcharr"
        minSdk = 29
        targetSdk = 36
        // CI passes -PversionName / -PversionCode (from the tag and run number).
        versionCode = providers.gradleProperty("versionCode").map(String::toInt).getOrElse(1)
        versionName = providers.gradleProperty("versionName").getOrElse("0.1.0")
    }

    signingConfigs {
        if (canSignRelease) {
            create("release") {
                storeFile = releaseKeystoreFile
                storePassword = releaseKeystorePassword.get()
                keyAlias = releaseKeyAlias.get()
                keyPassword = releaseKeyPassword.get()
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            if (canSignRelease) signingConfig = signingConfigs.getByName("release")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    testOptions {
        unitTests.isReturnDefaultValues = true
    }

    sourceSets {
        // Shared API contract fixtures live in the top-level /contract directory.
        // Exposed to unit tests as classpath resources (e.g. "README.md", "fixtures/...").
        getByName("test") {
            resources.directories.add("../../contract")
        }
    }

    lint {
        abortOnError = true
    }
}

kotlin {
    jvmToolchain(21)
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}

dependencies {
    implementation(platform(libs.androidx.compose.bom))
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.runtime.ktx)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.compose.ui)
    implementation(libs.androidx.compose.ui.tooling.preview)
    implementation(libs.androidx.compose.material3)
    implementation(libs.androidx.compose.material.icons.extended)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.androidx.datastore.preferences)

    implementation(libs.hilt.android)
    ksp(libs.hilt.compiler)
    implementation(libs.hilt.navigation.compose)

    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.retrofit)
    implementation(libs.retrofit.converter.kotlinx.serialization)
    implementation(libs.okhttp)
    implementation(libs.okhttp.logging.interceptor)

    debugImplementation(libs.androidx.compose.ui.tooling)

    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.turbine)
    testImplementation(libs.okhttp.mockwebserver)
}
