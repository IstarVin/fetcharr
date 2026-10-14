package com.istarvin.fetcharr.data.network

import kotlinx.serialization.json.Json
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Response
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import javax.inject.Inject
import javax.inject.Singleton

/** Adds `Authorization: Bearer <token>` when [token] is not blank. */
internal class AuthInterceptor(private val token: String?) : Interceptor {
    override fun intercept(chain: Interceptor.Chain): Response {
        val t = token?.trim()
        val request = if (t.isNullOrEmpty()) {
            chain.request()
        } else {
            chain.request().newBuilder().header("Authorization", "Bearer $t").build()
        }
        return chain.proceed(request)
    }
}

@Singleton
class FetcharrApiFactory @Inject constructor(
    private val baseClient: OkHttpClient,
    private val json: Json,
) {
    /** @throws IllegalArgumentException when [ServerConfig.baseUrl] is not a valid http(s) URL. */
    fun create(config: ServerConfig): FetcharrApi {
        val client = baseClient.newBuilder()
            .addInterceptor(AuthInterceptor(config.token))
            .build()
        return Retrofit.Builder()
            .baseUrl(config.baseUrl.trim().trimEnd('/') + "/api/")
            .client(client)
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(FetcharrApi::class.java)
    }
}

/** The [FetcharrApi] for the currently saved settings, cached per [ServerConfig]. */
@Singleton
class FetcharrApiProvider @Inject constructor(
    private val factory: FetcharrApiFactory,
    private val configProvider: ServerConfigProvider,
) {
    private var cached: Pair<ServerConfig, FetcharrApi>? = null

    /** Null when no server is configured. */
    @Synchronized
    fun current(): FetcharrApi? {
        val config = configProvider.config.value ?: return null
        cached?.let { (cfg, api) -> if (cfg == config) return api }
        return factory.create(config).also { cached = config to it }
    }
}
