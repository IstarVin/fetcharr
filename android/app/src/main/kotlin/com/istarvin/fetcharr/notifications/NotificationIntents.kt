package com.istarvin.fetcharr.notifications

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import com.istarvin.fetcharr.MainActivity
import com.istarvin.fetcharr.ui.navigation.OpenTarget

internal object NotificationIntents {
    const val EXTRA_OPEN = "open"
    const val EXTRA_JOB_ID = "retry_job_id"
    const val EXTRA_BATCH_ID = "retry_batch_id"
    const val EXTRA_NOTIFICATION_KEY = "notification_key"

    /**
     * Opens [MainActivity] on [target]. The request code differs per target because PendingIntents ignore extras
     * when matching. It carries no action or data, so the fetcharr:// intent handling ignores it.
     */
    fun open(context: Context, target: OpenTarget): PendingIntent {
        val intent = Intent(context, MainActivity::class.java)
            .putExtra(EXTRA_OPEN, target.extraValue)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
        return PendingIntent.getActivity(
            context,
            target.ordinal,
            intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
    }

    /** Broadcast to [RetryReceiver] for a single job or a whole batch. */
    fun retry(context: Context, key: String, jobId: String?, batchId: String?): PendingIntent {
        val intent = Intent(context, RetryReceiver::class.java)
            .putExtra(EXTRA_NOTIFICATION_KEY, key)
            .putExtra(EXTRA_JOB_ID, jobId)
            .putExtra(EXTRA_BATCH_ID, batchId)
        return PendingIntent.getBroadcast(
            context,
            key.hashCode(),
            intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
    }
}
