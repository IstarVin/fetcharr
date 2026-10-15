package com.istarvin.fetcharr.data.history

import com.istarvin.fetcharr.data.network.ApiError
import com.istarvin.fetcharr.data.network.ApiResult
import com.istarvin.fetcharr.data.network.FetcharrApi
import com.istarvin.fetcharr.data.network.FetcharrApiProvider
import com.istarvin.fetcharr.data.network.ServerConfigProvider
import com.istarvin.fetcharr.data.network.dto.JobDto
import com.istarvin.fetcharr.data.network.safeApiCall
import com.istarvin.fetcharr.data.sync.ActiveJobsStore
import com.istarvin.fetcharr.di.ApplicationScope
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.util.concurrent.atomic.AtomicBoolean
import javax.inject.Inject
import javax.inject.Singleton

/**
 * What the History screen shows.
 *
 * @property items finished jobs, newest `finished_at` first.
 * @property loaded true once the first page has arrived.
 * @property total the server's `total` for the finished jobs, kept in step with local inserts and removals.
 * @property nextOffset the `offset` of the next page: how many server rows are already loaded (adjusted for live
 * inserts and removals, so a page never skips rows; duplicates are dropped by id).
 */
data class HistoryState(
    val items: List<JobDto> = emptyList(),
    val loaded: Boolean = false,
    val loadingMore: Boolean = false,
    val endReached: Boolean = false,
    val error: ApiError? = null,
    val total: Int = 0,
    val nextOffset: Int = 0,
)

/**
 * Finished jobs from REST (`GET /api/jobs?status=completed,failed,cancelled`, `limit`/`offset` paging, 50 per page;
 * app spec §3.2, server §6.3), plus live insertion of jobs that finish while the app is open.
 *
 * Live insertion only happens once history has been loaded. A later [refresh] replaces everything with page one.
 * Switching to another server (different base URL) drops the cached history.
 */
@Singleton
class HistoryRepository @Inject constructor(
    private val apiProvider: FetcharrApiProvider,
    private val configProvider: ServerConfigProvider,
    store: ActiveJobsStore,
    @ApplicationScope scope: CoroutineScope,
) {
    private val _state = MutableStateFlow(HistoryState())
    val state: StateFlow<HistoryState> = _state.asStateFlow()

    /** Serialises network paging and resets. */
    private val mutex = Mutex()
    private val loadMoreRunning = AtomicBoolean(false)

    /** Base URL the current items came from. */
    private var sourceUrl: String? = null

    init {
        // UNDISPATCHED: subscribe before returning, so an event right after construction is not missed.
        scope.launch(start = CoroutineStart.UNDISPATCHED) { store.finished.collect(::onFinished) }
        scope.launch {
            configProvider.config.map { it?.baseUrl }.collect { url -> mutex.withLock { dropIfOtherServer(url) } }
        }
    }

    /** Loads the first page and replaces the list. Returns the error, or null on success. */
    suspend fun refresh(): ApiError? = mutex.withLock {
        val api = api() ?: return@withLock fail(ApiError.Unreachable)
        val url = configProvider.config.value?.baseUrl
        dropIfOtherServer(url)
        when (val result = safeApiCall { api.listJobs(status = STATUSES, limit = PAGE_SIZE) }) {
            is ApiResult.Success -> {
                val page = result.value
                sourceUrl = url
                _state.value = HistoryState(
                    items = merge(emptyList(), page.jobs),
                    loaded = true,
                    endReached = isEnd(page.jobs.size, page.jobs.size, page.total),
                    total = page.total,
                    nextOffset = page.jobs.size,
                )
                null
            }
            is ApiResult.Failure -> fail(result.error)
        }
    }

    /** Loads the next page and appends it. Does nothing before the first page or after the last. */
    suspend fun loadMore(): ApiError? {
        val before = _state.value
        if (!before.loaded || before.endReached || !loadMoreRunning.compareAndSet(false, true)) return null
        try {
            return mutex.withLock {
                val current = _state.value
                if (!current.loaded || current.endReached) return@withLock null
                val api = api() ?: return@withLock fail(ApiError.Unreachable)
                _state.update { it.copy(loadingMore = true, error = null) }
                when (val result = safeApiCall { api.listJobs(status = STATUSES, limit = PAGE_SIZE, offset = current.nextOffset) }) {
                    is ApiResult.Success -> {
                        val page = result.value
                        _state.update { s ->
                            val nextOffset = s.nextOffset + page.jobs.size
                            s.copy(
                                items = merge(s.items, page.jobs),
                                loadingMore = false,
                                endReached = isEnd(page.jobs.size, nextOffset, page.total),
                                total = page.total,
                                nextOffset = nextOffset,
                            )
                        }
                        null
                    }
                    is ApiResult.Failure -> fail(result.error)
                }
            }
        } finally {
            loadMoreRunning.set(false)
        }
    }

    /** Drops one job locally, e.g. after it was deleted or retried (it is active again). */
    fun remove(id: String) {
        _state.update { s ->
            if (s.items.none { it.id == id }) {
                s
            } else {
                s.copy(
                    items = s.items.filterNot { it.id == id },
                    total = (s.total - 1).coerceAtLeast(0),
                    nextOffset = (s.nextOffset - 1).coerceAtLeast(0),
                )
            }
        }
    }

    /** Empties the list locally (after `DELETE /api/jobs`). History stays "loaded", with nothing more to load. */
    fun clear() {
        _state.update { it.copy(items = emptyList(), loadingMore = false, endReached = true, error = null, total = 0, nextOffset = 0) }
    }

    private fun onFinished(job: JobDto) {
        _state.update { s ->
            if (!s.loaded) {
                s
            } else {
                val known = s.items.any { it.id == job.id }
                s.copy(
                    items = merge(s.items.filterNot { it.id == job.id }, listOf(job)),
                    total = if (known) s.total else s.total + 1,
                    nextOffset = if (known) s.nextOffset else s.nextOffset + 1,
                )
            }
        }
    }

    private fun dropIfOtherServer(url: String?) {
        val source = sourceUrl
        if (source != null && source != url) {
            sourceUrl = null
            _state.value = HistoryState()
        }
    }

    private fun fail(error: ApiError): ApiError {
        _state.update { it.copy(loadingMore = false, error = error) }
        return error
    }

    private fun api(): FetcharrApi? =
        try {
            apiProvider.current()
        } catch (e: IllegalArgumentException) {
            null // saved URL is not a valid http(s) URL
        }

    private fun isEnd(pageSize: Int, nextOffset: Int, total: Int): Boolean = pageSize < PAGE_SIZE || nextOffset >= total

    /** [incoming] replaces entries with the same id; the result is finished jobs only, newest first. */
    private fun merge(existing: List<JobDto>, incoming: List<JobDto>): List<JobDto> {
        val ids = incoming.mapTo(HashSet()) { it.id }
        return (incoming + existing.filterNot { it.id in ids })
            .filter { it.status.isFinished }
            .distinctBy { it.id }
            .sortedByDescending { it.finishedAt ?: it.createdAt }
    }

    companion object {
        const val PAGE_SIZE = 50
        const val STATUSES = "completed,failed,cancelled"
    }
}
