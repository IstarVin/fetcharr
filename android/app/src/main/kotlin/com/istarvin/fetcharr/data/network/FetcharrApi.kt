package com.istarvin.fetcharr.data.network

import com.istarvin.fetcharr.data.network.dto.BatchCreateResponse
import com.istarvin.fetcharr.data.network.dto.BatchJobsResponse
import com.istarvin.fetcharr.data.network.dto.ClearHistoryResponse
import com.istarvin.fetcharr.data.network.dto.CreateBatchRequest
import com.istarvin.fetcharr.data.network.dto.CreateJobRequest
import com.istarvin.fetcharr.data.network.dto.HealthDto
import com.istarvin.fetcharr.data.network.dto.InfoDto
import com.istarvin.fetcharr.data.network.dto.JobDto
import com.istarvin.fetcharr.data.network.dto.JobListDto
import com.istarvin.fetcharr.data.network.dto.RetryRequest
import com.istarvin.fetcharr.data.network.dto.YtdlpUpdateResponse
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.PUT
import com.istarvin.fetcharr.data.network.dto.ServerSettingsDto
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * Server §6.3 (the WebSocket is not here). Base URL is `<baseUrl>/api/`.
 * All methods throw on non-2xx (`retrofit2.HttpException`) and on I/O errors; wrap calls in [safeApiCall].
 */
interface FetcharrApi {
    @GET("system/settings")
    suspend fun serverSettings(): ServerSettingsDto

    @PUT("system/settings")
    suspend fun saveServerSettings(@Body settings: ServerSettingsDto): ServerSettingsDto

    @GET("health")
    suspend fun health(): HealthDto

    @GET("info")
    suspend fun info(): InfoDto

    @POST("jobs")
    suspend fun createJob(@Body request: CreateJobRequest): JobDto

    @POST("jobs/batch")
    suspend fun createBatch(@Body request: CreateBatchRequest): BatchCreateResponse

    /**
     * @param status comma-separated statuses, e.g. `"completed,failed,cancelled"`
     * @param finishedAfter RFC 3339 time, see `Rfc3339.format`
     */
    @GET("jobs")
    suspend fun listJobs(
        @Query("status") status: String? = null,
        @Query("category") category: String? = null,
        @Query("batch_id") batchId: String? = null,
        @Query("finished_after") finishedAfter: String? = null,
        @Query("limit") limit: Int? = null,
        @Query("offset") offset: Int? = null,
    ): JobListDto

    @GET("jobs/{id}")
    suspend fun getJob(@Path("id") id: String): JobDto

    @POST("jobs/{id}/pause")
    suspend fun pauseJob(@Path("id") id: String): JobDto

    @POST("jobs/{id}/resume")
    suspend fun resumeJob(@Path("id") id: String): JobDto

    @POST("jobs/{id}/cancel")
    suspend fun cancelJob(@Path("id") id: String): JobDto

    /** Retry without a body. */
    @POST("jobs/{id}/retry")
    suspend fun retryJob(@Path("id") id: String): JobDto

    /** Retry with a body, e.g. `RetryRequest(overwrite = true)` to replace an existing library file. */
    @POST("jobs/{id}/retry")
    suspend fun retryJobWithBody(@Path("id") id: String, @Body request: RetryRequest): JobDto

    /** `204 No Content`. [deleteFile] maps to `?delete_file=true` (completed jobs only). */
    @DELETE("jobs/{id}")
    suspend fun deleteJob(
        @Path("id") id: String,
        @Query("delete_file") deleteFile: Boolean? = null,
    )

    /** Clears finished jobs. [status] defaults to `completed,failed,cancelled` on the server. */
    @DELETE("jobs")
    suspend fun clearHistory(@Query("status") status: String? = null): ClearHistoryResponse

    @POST("batches/{batchId}/pause")
    suspend fun pauseBatch(@Path("batchId") batchId: String): BatchJobsResponse

    @POST("batches/{batchId}/resume")
    suspend fun resumeBatch(@Path("batchId") batchId: String): BatchJobsResponse

    @POST("batches/{batchId}/cancel")
    suspend fun cancelBatch(@Path("batchId") batchId: String): BatchJobsResponse

    @POST("batches/{batchId}/retry")
    suspend fun retryBatch(@Path("batchId") batchId: String): BatchJobsResponse

    @POST("system/ytdlp/update")
    suspend fun updateYtdlp(): YtdlpUpdateResponse
}
