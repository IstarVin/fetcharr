package dev.istarvin.fetcharr.incoming

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** One episode in a season sheet, with its own headers/subtitles and selection flag. */
data class SeasonEpisode(
    val episode: Int,
    val title: String?,
    val url: String,
    val headers: Map<String, String>,
    val subtitles: List<IncomingSubtitle>,
    val selected: Boolean,
)

sealed interface IncomingSheet {
    /** Unique per opened/queued sheet; lets the UI key its editable state to a sheet. */
    val id: Long

    /** A movie or flat item (any non-episodic single). */
    data class SingleSheet(override val id: Long, val request: IncomingRequest.Single) : IncomingSheet

    /**
     * An episodic request (single episode, several, or a batch). A sheet with one episode is shown by the UI
     * with the single-item layout; [showName], [season], [year] and [categoryHint] are as first received.
     */
    data class SeasonSheet(
        override val id: Long,
        val showName: String,
        val season: Int,
        val year: Int?,
        val categoryHint: String?,
        /** Sorted by episode number. */
        val episodes: List<SeasonEpisode>,
    ) : IncomingSheet {
        val selectedCount: Int get() = episodes.count { it.selected }
    }
}

data class IncomingState(
    val sheet: IncomingSheet? = null,
    /** Requests queued behind the open sheet ("1 more request waiting"). */
    val waitingCount: Int = 0,
    /** True while an arrival into the open season sheet happened within the receiving window; disables Download. */
    val receiving: Boolean = false,
)

/**
 * Owns the incoming-request queue, season aggregation (spec 4.3) and the "receiving" window.
 * Time is driven by `delay` inside [scope], so tests can use virtual time.
 * Not Android-specific; all methods are safe to call from any thread.
 */
class IncomingCoordinator(
    private val scope: CoroutineScope,
    private val receivingWindowMs: Long = DEFAULT_RECEIVING_WINDOW_MS,
) {
    private class Pending(var sheet: IncomingSheet, var startsReceiving: Boolean)

    private val lock = Any()
    private val queue = ArrayDeque<Pending>()
    private var nextId = 1L
    private var windowGeneration = 0L
    private var windowJob: Job? = null
    private var submittingSheetId: Long? = null

    private val _state = MutableStateFlow(IncomingState())
    val state: StateFlow<IncomingState> = _state.asStateFlow()

    /**
     * Opens, merges or queues [request].
     * Merge rule: the open sheet is a season sheet of the same show (trimmed, case-insensitive) and season and
     * the request is episodic. Otherwise it is queued; a queued season sheet of the same show and season absorbs
     * it instead of adding a new queue entry.
     */
    fun onRequest(request: IncomingRequest) {
        synchronized(lock) {
            val open = _state.value.sheet
            val key = request.seasonKey()
            if (open == null) {
                val pending = newPending(request)
                openLocked(pending)
            } else if (open is IncomingSheet.SeasonSheet && key != null && open.key() == key && submittingSheetId != open.id) {
                _state.value = _state.value.copy(sheet = mergeInto(open, request))
                restartWindowLocked()
            } else {
                val queued = key?.let { k ->
                    queue.lastOrNull { (it.sheet as? IncomingSheet.SeasonSheet)?.key() == k }
                }
                if (queued != null) {
                    queued.sheet = mergeInto(queued.sheet as IncomingSheet.SeasonSheet, request)
                    queued.startsReceiving = true
                } else {
                    queue.addLast(newPending(request))
                }
                _state.value = _state.value.copy(waitingCount = queue.size)
            }
        }
    }

    /**
     * Locks the open sheet [sheetId] while its episodes are being submitted and returns its current content (the
     * snapshot to submit), or null when [sheetId] is not the open sheet. While locked, new requests for the same
     * season are queued as their own request instead of being merged, so they are not lost when the sheet closes.
     * Selection edits are ignored while locked. [close] and [endSubmit] release the lock.
     */
    fun beginSubmit(sheetId: Long): IncomingSheet? {
        synchronized(lock) {
            val open = _state.value.sheet ?: return null
            if (open.id != sheetId) return null
            submittingSheetId = sheetId
            return open
        }
    }

    /** Releases the lock taken by [beginSubmit] (after a failed submit); merging resumes. */
    fun endSubmit(sheetId: Long) {
        synchronized(lock) {
            if (submittingSheetId == sheetId) submittingSheetId = null
        }
    }

    /** Flips the selection of [episode] in the open season sheet. No-op if absent. */
    fun toggleEpisode(episode: Int) {
        updateOpenSeason { sheet ->
            sheet.copy(
                episodes = sheet.episodes.map { if (it.episode == episode) it.copy(selected = !it.selected) else it },
            )
        }
    }

    fun setEpisodeSelected(episode: Int, selected: Boolean) {
        updateOpenSeason { sheet ->
            sheet.copy(
                episodes = sheet.episodes.map { if (it.episode == episode) it.copy(selected = selected) else it },
            )
        }
    }

    /** Selects or deselects every episode of the open season sheet. */
    fun setAllSelected(selected: Boolean) {
        updateOpenSeason { sheet -> sheet.copy(episodes = sheet.episodes.map { it.copy(selected = selected) }) }
    }

    /** Dismisses the open sheet (cancel or after a successful submit) and opens the next queued request. */
    fun close() {
        synchronized(lock) {
            submittingSheetId = null
            cancelWindowLocked()
            val next = queue.removeFirstOrNull()
            if (next == null) {
                _state.value = IncomingState()
            } else {
                openLocked(next)
            }
        }
    }

    // ---- internals --------------------------------------------------------------------------

    private fun updateOpenSeason(transform: (IncomingSheet.SeasonSheet) -> IncomingSheet.SeasonSheet) {
        synchronized(lock) {
            val open = _state.value.sheet as? IncomingSheet.SeasonSheet ?: return
            if (submittingSheetId == open.id) return
            _state.value = _state.value.copy(sheet = transform(open))
        }
    }

    private fun IncomingSheet.SeasonSheet.key() = SeasonKey.of(showName, season)

    private fun newPending(request: IncomingRequest): Pending {
        val id = nextId++
        return when (request) {
            is IncomingRequest.Single ->
                if (request.isEpisodic) {
                    // A lone episode may be the first of many: start the receiving window when it opens.
                    Pending(seasonSheetOf(id, request), startsReceiving = true)
                } else {
                    Pending(IncomingSheet.SingleSheet(id, request), startsReceiving = false)
                }
            // A batch is complete in itself, so it does not start the window until something merges into it.
            is IncomingRequest.Batch -> Pending(seasonSheetOf(id, request), startsReceiving = false)
        }
    }

    private fun seasonSheetOf(id: Long, request: IncomingRequest): IncomingSheet.SeasonSheet {
        val (show, season, year) = when (request) {
            is IncomingRequest.Batch -> Triple(request.showName, request.season, request.year)
            is IncomingRequest.Single -> Triple(request.showName.orEmpty(), request.season ?: 0, request.year)
        }
        val entries = request.toEpisodeEntries().map { it.toSeasonEpisode() }.sortedBy { it.episode }
        return IncomingSheet.SeasonSheet(id, show, season, year, request.categoryHint, entries)
    }

    private fun mergeInto(sheet: IncomingSheet.SeasonSheet, request: IncomingRequest): IncomingSheet.SeasonSheet {
        val byNumber = sheet.episodes.associateByTo(LinkedHashMap()) { it.episode }
        for (entry in request.toEpisodeEntries()) byNumber[entry.episode] = entry.toSeasonEpisode()
        val requestYear = when (request) {
            is IncomingRequest.Batch -> request.year
            is IncomingRequest.Single -> request.year
        }
        return sheet.copy(
            year = sheet.year ?: requestYear,
            categoryHint = sheet.categoryHint ?: request.categoryHint,
            episodes = byNumber.values.sortedBy { it.episode },
        )
    }

    private fun EpisodeEntry.toSeasonEpisode() = SeasonEpisode(episode, title, url, headers, subtitles, selected = true)

    private fun openLocked(pending: Pending) {
        _state.value = IncomingState(sheet = pending.sheet, waitingCount = queue.size, receiving = false)
        if (pending.startsReceiving) restartWindowLocked() else cancelWindowLocked()
    }

    private fun restartWindowLocked() {
        windowJob?.cancel()
        val generation = ++windowGeneration
        _state.value = _state.value.copy(receiving = true)
        windowJob = scope.launch {
            delay(receivingWindowMs)
            synchronized(lock) {
                if (generation == windowGeneration) {
                    _state.value = _state.value.copy(receiving = false)
                }
            }
        }
    }

    private fun cancelWindowLocked() {
        windowGeneration++
        windowJob?.cancel()
        windowJob = null
        if (_state.value.receiving) _state.value = _state.value.copy(receiving = false)
    }

    companion object {
        const val DEFAULT_RECEIVING_WINDOW_MS = 2_000L
    }
}
