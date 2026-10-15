package com.istarvin.fetcharr.notifications

import com.istarvin.fetcharr.data.network.dto.JobDto
import com.istarvin.fetcharr.notifications.logic.NotifierState
import com.istarvin.fetcharr.notifications.logic.ResultNotification

// Small seams so the background and notification logic runs on the JVM with fakes. The Android
// implementations live in SystemNotifier, AndroidServiceStarter and the DataStore-backed stores.

/** Wall-clock time in epoch milliseconds. */
fun interface TimeSource {
    fun nowMs(): Long
}

/**
 * Starts [SyncForegroundService]. There is no stop: the service stops itself when
 * [BackgroundController.shouldRun] turns false, after it has called `startForeground`.
 */
fun interface ServiceStarter {
    /** True when the start request was accepted; false when the system refused it (for example from the background). */
    fun start(): Boolean
}

/** Posts the "Stopped tracking downloads" notice on the `results` channel. */
fun interface StoppedNoticePoster {
    fun postStoppedNotice(text: String)
}

/** Posts a result notification on the `results` channel. Does nothing when notifications are not allowed. */
fun interface ResultPoster {
    fun post(notification: ResultNotification)
}

/** Called after a job was created or retried successfully. */
fun interface SubmitListener {
    fun onSubmitSucceeded()
}

/** The REST calls the result notifier needs. Both return null when the call failed (no network, no server, ...). */
interface JobsGateway {
    /** All completed, failed and cancelled jobs with `finished_at` later than [afterMs], every page. */
    suspend fun listFinishedAfter(afterMs: Long): List<JobDto>?

    /** Every job of [batchId]. */
    suspend fun listBatch(batchId: String): List<JobDto>?
}

/** Persistence of the notifier's de-duplication state. */
interface NotifierStateStore {
    suspend fun load(): NotifierState

    suspend fun save(state: NotifierState)

    /** Identifies the server the state and last-seen belong to (its base URL); null before the first run. */
    suspend fun loadServerKey(): String?

    suspend fun saveServerKey(key: String)
}

/** Newest `finished_at` the notifier has processed (epoch ms). */
interface LastSeenStore {
    suspend fun get(): Long?

    suspend fun set(epochMs: Long)

    /** Sets [nowMs] unless a value is already stored. */
    suspend fun initIfAbsent(nowMs: Long)
}
