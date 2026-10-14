package dev.istarvin.fetcharr.notifications

import android.Manifest
import android.app.Notification
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat

internal object NotificationIds {
    const val ONGOING = 1
    const val STOPPED = 2

    /** Result notifications are told apart by tag (the job id or "batch:<id>"). */
    const val RESULT = 3
}

/**
 * The one place that posts notifications. Never throws: without the POST_NOTIFICATIONS permission (Android 13+) or
 * with notifications switched off it quietly does nothing.
 */
internal fun postNotification(context: Context, tag: String?, id: Int, notification: Notification) {
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
        ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) !=
        PackageManager.PERMISSION_GRANTED
    ) {
        return
    }
    val manager = NotificationManagerCompat.from(context)
    if (!manager.areNotificationsEnabled()) return
    try {
        manager.notify(tag, id, notification)
    } catch (e: SecurityException) {
        // Permission revoked between the check and the call.
    }
}
