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
    /** The filter and search the items were loaded for. */
    val query: HistoryQuery = HistoryQuery(),
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
        val query = _state.value.query
        when (val result = safeApiCall { api.listJobs(status = query.statuses, query = query.text.trim().ifEmpty { null }, limit = PAGE_SIZE) }) {
            is ApiResult.Success -> {
                if (_state.value.query != query) return@withLock null // a newer filter or search is loading
                val page = result.value
                sourceUrl = url
                _state.value = HistoryState(
                    items = merge(emptyList(), page.jobs, query),
                    loaded = true,
                    endReached = isEnd(page.jobs.size, page.jobs.size, page.total),
                    total = page.total,
                    nextOffset = page.jobs.size,
                    query = query,
                )
                null
            }
            is ApiResult.Failure -> fail(result.error)
        }
    }

    /**
     * Applies a new filter or search and reloads page one. The current rows stay visible until the new page
     * arrives; jobs finishing meanwhile already follow [query].
     */
    suspend fun setQuery(query: HistoryQuery): ApiError? {
        if (_state.value.query == query) return null
        _state.update { it.copy(query = query, items = it.items.filter(query::accepts)) }
        return refresh()
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
                val query = current.query
                val result = safeApiCall {
                    api.listJobs(status = query.statuses, query = query.text.trim().ifEmpty { null }, limit = PAGE_SIZE, offset = current.nextOffset)
                }
                if (_state.value.query != query) {
                    _state.update { it.copy(loadingMore = false) }
                    return@withLock null
                }
                when (result) {
                    is ApiResult.Success -> {
                        val page = result.value
                        _state.update { s ->
                            val nextOffset = s.nextOffset + page.jobs.size
                            s.copy(
                                items = merge(s.items, page.jobs, query),
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
        if (!_state.value.query.accepts(job)) {
            remove(job.id)
            return
        }
        _state.update { s ->
            if (!s.loaded) {
                s
            } else {
                val known = s.items.any { it.id == job.id }
                s.copy(
                    items = merge(s.items.filterNot { it.id == job.id }, listOf(job), s.query),
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

    /** [incoming] replaces entries with the same id; the result is finished jobs matching [query] only, newest first. */
    private fun merge(existing: List<JobDto>, incoming: List<JobDto>, query: HistoryQuery): List<JobDto> {
        val ids = incoming.mapTo(HashSet()) { it.id }
        return (incoming + existing.filterNot { it.id in ids })
            .filter { it.status.isFinished && it.matchesStatus(query) }
            .distinctBy { it.id }
            .sortedByDescending { it.finishedAt ?: it.createdAt }
    }

    /** The status always matches (the server filtered it); the text only when the server supports search. */
    private fun JobDto.matchesStatus(query: HistoryQuery): Boolean = query.copy(text = "").accepts(this)

    companion object {
        const val PAGE_SIZE = 50
        const val STATUSES = "completed,failed,cancelled"
    }
}
