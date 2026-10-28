package com.istarvin.fetcharr.ui.incoming

import com.istarvin.fetcharr.data.network.ApiError
import com.istarvin.fetcharr.data.network.ApiResult
import com.istarvin.fetcharr.data.network.FetcharrApi
import com.istarvin.fetcharr.data.network.FetcharrApiProvider
import com.istarvin.fetcharr.data.network.dto.BatchEpisodeRequest
import com.istarvin.fetcharr.data.network.dto.CreateBatchRequest
import com.istarvin.fetcharr.data.network.dto.CreateJobRequest
import com.istarvin.fetcharr.data.network.dto.SkippedEpisodeDto
import com.istarvin.fetcharr.data.network.dto.SubtitleDto
import com.istarvin.fetcharr.data.network.safeApiCall
import com.istarvin.fetcharr.incoming.CategoryOption
import com.istarvin.fetcharr.incoming.IncomingSubtitle
import com.istarvin.fetcharr.incoming.SeasonEpisode
import javax.inject.Inject
import javax.inject.Singleton

sealed interface SubmitOutcome {
    /** [message] is the snackbar text; the caller closes the sheet / screen. */
    data class Success(val message: String) : SubmitOutcome

    /**
     * `409 duplicate`: the snackbar reads [MESSAGE_DUPLICATE] and the sheet closes. Also used when a batch
     * reports every episode as already queued (the server answers that with `409 duplicate` too).
     */
    data object DuplicateSingle : SubmitOutcome

    /**
     * `409 already_exists`: the library already has the file (for a batch: every selected episode is in the
     * library or queued). The caller asks whether to replace it and, if so, submits again with `overwrite`.
     */
    data object AlreadyExists : SubmitOutcome

    /** Shown inline; the sheet or screen stays open so the user can retry. */
    data class Error(val message: String) : SubmitOutcome

    data object NotConfigured : SubmitOutcome

    companion object {
        const val MESSAGE_DUPLICATE = "Already in the queue"
        const val MESSAGE_ALREADY_EXISTS = "Already in library"
        const val MESSAGE_NOT_CONFIGURED = "Server not set up"
    }
}

/**
 * Turns confirmed form data into `POST /api/jobs` or `POST /api/jobs/batch` calls (app spec §5.1, §5.2, §7).
 * No Compose or Android dependencies. Request headers are passed straight to the request body: they are never
 * stored, logged or put in a message.
 */
@Singleton
class SubmitUseCase @Inject constructor(private val apiProvider: FetcharrApiProvider) {

    /**
     * Submits one item with `POST /jobs`.
     * The fields sent follow [layout]: movie = title + year; flat = title; tv = show_name, season, episode,
     * year and (when non-blank) the episode title as `title`. [overwrite] replaces a file already in the library.
     */
    suspend fun submitJob(
        url: String,
        category: String,
        layout: String,
        form: ItemForm,
        headers: Map<String, String>,
        subtitles: List<SubtitleDto>,
        overwrite: Boolean = false,
    ): SubmitOutcome {
        val api = api() ?: return SubmitOutcome.NotConfigured
        val request = buildJobRequest(url, category, layout, form, headers, subtitles)
            .copy(overwrite = overwrite.takeIf { it })
        return when (val result = safeApiCall { api.createJob(request) }) {
            is ApiResult.Success -> SubmitOutcome.Success(addedMessage(1))
            is ApiResult.Failure -> failure(result.error)
        }
    }

    /**
     * Submits the selected episodes of a season sheet. One episode goes to `POST /jobs` (with the season's
     * show/season and that episode's number and title); two or more go to `POST /jobs/batch`.
     * Only the show, season and year fields of [form] are used. Without [overwrite], episodes already in the
     * library are skipped by the server and counted in the message.
     */
    suspend fun submitSeason(
        category: String,
        form: ItemForm,
        episodes: List<SeasonEpisode>,
        overwrite: Boolean = false,
    ): SubmitOutcome {
        val selected = episodes.filter { it.selected }
        if (selected.isEmpty()) return SubmitOutcome.Error("Nothing selected")
        if (selected.size == 1) {
            val only = selected.single()
            val single = form.copy(episode = only.episode.toString(), episodeTitle = only.title.orEmpty())
            return submitJob(
                only.url, category, CategoryOption.LAYOUT_TV, single, only.headers,
                only.subtitles.map { SubtitleDto(it.url, it.lang) }, overwrite,
            )
        }
        val api = api() ?: return SubmitOutcome.NotConfigured
        val request = CreateBatchRequest(
            category = category,
            showName = form.show.trim(),
            season = form.seasonValue ?: 0,
            year = form.yearValue,
            headers = null, // each episode carries its own (already merged with the payload's root headers)
            episodes = selected.map {
                BatchEpisodeRequest(
                    episode = it.episode,
                    title = it.title?.takeIf { t -> t.isNotBlank() },
                    url = it.url,
                    subtitles = it.subtitles.toDtos(),
                    headers = it.headers.cleaned(),
                )
            },
            overwrite = overwrite.takeIf { it },
        )
        return when (val result = safeApiCall { api.createBatch(request) }) {
            is ApiResult.Success -> SubmitOutcome.Success(batchMessage(result.value.jobs.size, result.value.skipped))
            is ApiResult.Failure -> failure(result.error)
        }
    }

    private fun api(): FetcharrApi? = try {
        apiProvider.current()
    } catch (_: IllegalArgumentException) {
        null // a saved URL the HTTP client rejects counts as "not set up"
    }

    private fun failure(error: ApiError): SubmitOutcome = when (error) {
        is ApiError.Duplicate -> SubmitOutcome.DuplicateSingle
        is ApiError.AlreadyExists -> SubmitOutcome.AlreadyExists
        else -> SubmitOutcome.Error(error.userMessage)
    }

    companion object {
        /**
         * "Added 1 download" / "Added N downloads". The spec's example only shows the plural form; the singular
         * is used for exactly one job.
         */
        fun addedMessage(count: Int): String = if (count == 1) "Added 1 download" else "Added $count downloads"

        /** "Added N downloads", or "Added N · M already in library · K already queued" when episodes were skipped. */
        internal fun batchMessage(created: Int, skipped: List<SkippedEpisodeDto>): String {
            if (skipped.isEmpty()) return addedMessage(created)
            val inLibrary = skipped.count { it.reason == SKIP_ALREADY_EXISTS }
            val queued = skipped.size - inLibrary
            return buildList {
                add("Added $created")
                if (inLibrary > 0) add("$inLibrary already in library")
                if (queued > 0) add("$queued already queued")
            }.joinToString(" · ")
        }

        private const val SKIP_ALREADY_EXISTS = "already_exists"

        internal fun buildJobRequest(
            url: String,
            category: String,
            layout: String,
            form: ItemForm,
            headers: Map<String, String>,
            subtitles: List<SubtitleDto>,
        ): CreateJobRequest {
            val base = CreateJobRequest(
                url = url.trim(),
                category = category,
                headers = headers.cleaned(),
                subtitles = subtitles.takeIf { it.isNotEmpty() }?.map { SubtitleDto(it.url.trim(), it.lang?.trim()?.takeIf { l -> l.isNotEmpty() }) },
            )
            return when (layout) {
                CategoryOption.LAYOUT_MOVIE -> base.copy(title = form.title.trim(), year = form.yearValue)
                CategoryOption.LAYOUT_TV -> base.copy(
                    title = form.episodeTitle.trim().takeIf { it.isNotEmpty() },
                    showName = form.show.trim(),
                    season = form.seasonValue,
                    episode = form.episodeValue,
                    year = form.yearValue,
                )
                else -> base.copy(title = form.title.trim())
            }
        }

        private fun Map<String, String>.cleaned(): Map<String, String>? =
            entries.filter { it.key.isNotBlank() }.associate { it.key.trim() to it.value }.takeIf { it.isNotEmpty() }

        private fun List<IncomingSubtitle>.toDtos(): List<SubtitleDto>? =
            takeIf { it.isNotEmpty() }?.map { SubtitleDto(it.url, it.lang) }
    }
}
