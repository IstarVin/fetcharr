package com.istarvin.fetcharr.notifications

import android.content.Context
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import dagger.hilt.android.qualifiers.ApplicationContext
import com.istarvin.fetcharr.R
import com.istarvin.fetcharr.notifications.logic.ResultNotification
import com.istarvin.fetcharr.ui.navigation.OpenTarget
import javax.inject.Inject
import javax.inject.Singleton

/** Posts result notifications and the "stopped tracking" notice on the `results` channel. */
@Singleton
class SystemNotifier @Inject constructor(
    @ApplicationContext private val context: Context,
) : ResultPoster, StoppedNoticePoster {

    override fun post(notification: ResultNotification) {
        val builder = NotificationCompat.Builder(context, NotificationChannels.RESULTS)
            .setSmallIcon(R.drawable.ic_stat_fetcharr)
            .setContentTitle(notification.title)
            .setContentText(notification.text)
            .setStyle(NotificationCompat.BigTextStyle().bigText(notification.text))
            .setCategory(NotificationCompat.CATEGORY_STATUS)
            .setAutoCancel(true)
            .setContentIntent(NotificationIntents.open(context, OpenTarget.HISTORY))
        if (notification.retryJobId != null || notification.retryBatchId != null) {
            builder.addAction(
                0,
                context.getString(R.string.notification_retry),
                NotificationIntents.retry(context, notification.key, notification.retryJobId, notification.retryBatchId),
            )
        }
        postNotification(context, notification.key, NotificationIds.RESULT, builder.build())
    }

    override fun postStoppedNotice(text: String) {
        val notification = NotificationCompat.Builder(context, NotificationChannels.RESULTS)
            .setSmallIcon(R.drawable.ic_stat_fetcharr)
            .setContentTitle(text)
            .setCategory(NotificationCompat.CATEGORY_STATUS)
            .setAutoCancel(true)
            .setContentIntent(NotificationIntents.open(context, OpenTarget.QUEUE))
            .build()
        postNotification(context, null, NotificationIds.STOPPED, notification)
    }

    /** Dismisses the result notification with [key]. */
    fun cancelResult(key: String) {
        NotificationManagerCompat.from(context).cancel(key, NotificationIds.RESULT)
    }
}
