package com.istarvin.fetcharr.data

import com.istarvin.fetcharr.data.network.ApiResult
import com.istarvin.fetcharr.data.network.FetcharrApiFactory
import com.istarvin.fetcharr.data.network.ServerConfig
import com.istarvin.fetcharr.data.network.dto.InfoDto
import com.istarvin.fetcharr.data.network.dto.ServerSettingsDto
import com.istarvin.fetcharr.data.network.safeApiCall
import javax.inject.Inject
import javax.inject.Singleton

/** Explicit connection arguments keep every edit bound to the server it was loaded from. */
@Singleton
class ServerSettingsRepository @Inject constructor(private val factory: FetcharrApiFactory) {
    suspend fun info(connection: ServerConfig): ApiResult<InfoDto> =
        safeApiCall { factory.create(connection).info() }

    suspend fun load(connection: ServerConfig): ApiResult<ServerSettingsDto> =
        safeApiCall { factory.create(connection).serverSettings() }

    suspend fun save(connection: ServerConfig, document: ServerSettingsDto): ApiResult<ServerSettingsDto> =
        safeApiCall { factory.create(connection).saveServerSettings(document) }
}
