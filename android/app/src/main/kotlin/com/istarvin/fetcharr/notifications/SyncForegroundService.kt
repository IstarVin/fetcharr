package com.istarvin.fetcharr.notifications

import android.app.Notification
import android.app.Service
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.IBinder
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import dagger.hilt.android.AndroidEntryPoint
import com.istarvin.fetcharr.R
import com.istarvin.fetcharr.data.network.dto.JobDto
import com.istarvin.fetcharr.data.sync.ActiveJobsStore
import com.istarvin.fetcharr.data.sync.SyncManager
import com.istarvin.fetcharr.notifications.logic.OngoingNotificationText
import com.istarvin.fetcharr.ui.navigation.OpenTarget
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * `dataSync` foreground service: holds the WebSocket open ([SyncManager.acquire]) while downloads run and shows
 * "Downloading 2 · 3 queued" with progress. When it starts and stops is decided by [BackgroundController].
 */
@AndroidEntryPoint
class SyncForegroundService : Service() {
    @Inject lateinit var store: ActiveJobsStore

    @Inject lateinit var syncManager: SyncManager

    @Inject lateinit var controller: BackgroundController

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private var updates: Job? = null
    private var stopWatcher: Job? = null
    private var acquired = false
    private var latestStartId = 0

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        latestStartId = startId
        NotificationChannels.create(this)
        try {
            ServiceCompat.startForeground(
                this,
                NotificationIds.ONGOING,
                buildNotification(store.jobs.value),
                ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC,
            )
        } catch (e: IllegalStateException) {
            // Not allowed to run as a foreground service right now (for example the Android 15 dataSync budget is
            // used up). Same outcome as a timeout: stop and tell the user.
            giveUp()
            return START_NOT_STICKY
        } catch (e: SecurityException) {
            giveUp()
            return START_NOT_STICKY
        }
        // Always call startForeground first (above), even when the job is already gone: stopping before it makes
        // the system crash the app. Only now is it safe to look at whether we are still wanted.
        if (!controller.shouldRun.value) {
            stopSelf(startId)
            return START_NOT_STICKY
        }
        if (!acquired) {
            acquired = true
            syncManager.acquire(SYNC_REASON)
        }
        if (stopWatcher == null) {
            // stopSelf(id) is a no-op when a newer start command arrived meanwhile.
            stopWatcher = scope.launch {
                while (true) {
                    controller.shouldRun.first { !it }
                    stopSelf(latestStartId)
                    controller.shouldRun.first { it } // only reached when a newer start kept us alive
                }
            }
        }
        if (updates == null) {
            updates = scope.launch {
                store.jobs.collect { jobs ->
                    postNotification(this@SyncForegroundService, null, NotificationIds.ONGOING, buildNotification(jobs))
                }
            }
        }
        return START_NOT_STICKY
    }

    /** Android 15+ dataSync limit reached: the service must stop within seconds. */
    override fun onTimeout(startId: Int, fgsType: Int) {
        giveUp()
    }

    /** Android 14 variant (short services); harmless for dataSync. */
    override fun onTimeout(startId: Int) {
        giveUp()
    }

    /** The controller posts the "stopped tracking" notice and stops us; stopSelf makes sure we end either way. */
    private fun giveUp() {
        controller.onSystemTimeout()
        stopSelf()
    }

    override fun onDestroy() {
        updates?.cancel()
        stopWatcher?.cancel()
        scope.cancel()
        if (acquired) {
            acquired = false
            syncManager.release(SYNC_REASON)
        }
        super.onDestroy()
    }

    private fun buildNotification(jobs: List<JobDto>): Notification {
        val content = OngoingNotificationText.content(jobs)
        val builder = NotificationCompat.Builder(this, NotificationChannels.ACTIVE)
            .setSmallIcon(R.drawable.ic_stat_fetcharr)
            .setContentTitle(content.text)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setSilent(true)
            .setShowWhen(false)
            .setCategory(NotificationCompat.CATEGORY_PROGRESS)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
            .setContentIntent(NotificationIntents.open(this, OpenTarget.QUEUE))
        val percent = content.progressPercent
        if (percent != null) builder.setProgress(100, percent, false) else builder.setProgress(0, 0, true)
        return builder.build()
    }

    private companion object {
        const val SYNC_REASON = "service"
    }
}
