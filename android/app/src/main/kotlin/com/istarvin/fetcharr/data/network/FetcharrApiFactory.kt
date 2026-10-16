package com.istarvin.fetcharr.data.network

import com.istarvin.fetcharr.di.ApplicationScope
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.launch
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

/** A captured connection. Identity distinguishes revisiting the same URL/token from the original connection. */
class FetcharrApiSession internal constructor(val config: ServerConfig, val api: FetcharrApi)

/** The [FetcharrApi] for the currently saved settings, cached for the lifetime of each connection. */
@Singleton
class FetcharrApiProvider @Inject constructor(
    private val factory: FetcharrApiFactory,
    private val configProvider: ServerConfigProvider,
    @ApplicationScope scope: CoroutineScope = CoroutineScope(SupervisorJob()),
) {
    private var observedConfig: ServerConfig? = configProvider.config.value
    private var cached: FetcharrApiSession? = null

    init {
        // Observe settings at emission time, including A -> B -> A between API calls. Running undispatched
        // also establishes the subscription before a caller can capture its first session.
        scope.launch(Dispatchers.Unconfined, start = CoroutineStart.UNDISPATCHED) {
            configProvider.config.collect { config -> synchronized(this@FetcharrApiProvider) { observe(config) } }
        }
    }

    /** Null when no server is configured. */
    fun current(): FetcharrApi? = captureSession()?.api

    /** Capture once before starting a request; validate before applying its result. */
    @Synchronized
    fun captureSession(): FetcharrApiSession? {
        observe(configProvider.config.value)
        val config = observedConfig ?: return null
        return cached ?: FetcharrApiSession(config, factory.create(config)).also { cached = it }
    }

    @Synchronized
    fun isCurrent(session: FetcharrApiSession): Boolean {
        observe(configProvider.config.value)
        return cached === session
    }

    /** Apply synchronous effects only while [session] is current; null means its connection was replaced. */
    @Synchronized
    fun <T : Any> withCurrentSession(session: FetcharrApiSession, block: () -> T): T? =
        if (isCurrent(session)) block() else null

    /** Orders connection replacement effects against results already being applied by a captured session. */
    @Synchronized
    fun <T : Any> withCurrentConfig(config: ServerConfig?, block: () -> T): T? {
        val current = configProvider.config.value
        observe(current)
        return if (config == current) block() else null
    }

    private fun observe(config: ServerConfig?) {
        if (observedConfig != config) {
            observedConfig = config
            cached = null
        }
    }
}
