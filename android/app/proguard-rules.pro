# Fetcharr release (R8) rules. Hilt, Compose, coroutines, DataStore and AndroidX ship their own consumer rules.

# ---- kotlinx.serialization (official app rules) ----------------------------------------------------
# Keep annotations and signatures that the runtime reads reflectively.
-keepattributes *Annotation*, InnerClasses, RuntimeVisibleAnnotations, AnnotationDefault, Signature
-dontnote kotlinx.serialization.AnnotationsKt

# Serializer for classes with named companion members and for the library's own JSON types.
-keepclassmembers class kotlinx.serialization.json.** {
    *** Companion;
}
-keepclasseswithmembers class kotlinx.serialization.json.** {
    kotlinx.serialization.KSerializer serializer(...);
}

# @Serializable classes: keep the Companion.serializer() lookup and the generated $$serializer.
-if @kotlinx.serialization.Serializable class ** {
    static **$* *;
}
-keepclassmembers class <2>$<3> {
    kotlinx.serialization.KSerializer serializer(...);
}
-if @kotlinx.serialization.Serializable class ** {
    public static ** INSTANCE;
}
-keepclassmembers class <1> {
    public static <1> INSTANCE;
    kotlinx.serialization.KSerializer serializer(...);
}
-if @kotlinx.serialization.Serializable class ** {
    *** Companion;
}
-keepclassmembers class <1> {
    *** Companion;
    kotlinx.serialization.KSerializer serializer(...);
}
-keep,includedescriptorclasses class **$$serializer { *; }

# DTOs: the wire names live in @SerialName strings, so members may be renamed, but the generated
# serializers and constructors must survive.
-keepclassmembers @kotlinx.serialization.Serializable class com.istarvin.fetcharr.data.network.dto.** {
    <init>(...);
}

# ---- Retrofit 3 -------------------------------------------------------------------------------------
-keepattributes Signature, InnerClasses, EnclosingMethod, RuntimeVisibleAnnotations, RuntimeVisibleParameterAnnotations
-keepattributes AnnotationDefault
-keepclassmembers,allowshrinking,allowobfuscation interface * {
    @retrofit2.http.* <methods>;
}
-keep,allowobfuscation,allowshrinking interface com.istarvin.fetcharr.data.network.FetcharrApi
# Suspend functions: Retrofit reads the Continuation generic argument and retrofit2.Response.
-keep,allowobfuscation,allowshrinking class kotlin.coroutines.Continuation
-keep,allowobfuscation,allowshrinking class retrofit2.Response
-dontwarn retrofit2.KotlinExtensions
-dontwarn retrofit2.KotlinExtensions$*
-dontwarn javax.annotation.**

# ---- OkHttp 5 / Okio --------------------------------------------------------------------------------
-dontwarn okhttp3.internal.platform.**
-dontwarn org.conscrypt.**
-dontwarn org.bouncycastle.**
-dontwarn org.openjsse.**
-dontwarn okio.**
