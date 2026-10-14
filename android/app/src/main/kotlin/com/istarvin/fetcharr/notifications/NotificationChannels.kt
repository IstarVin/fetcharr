package com.istarvin.fetcharr.notifications

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import com.istarvin.fetcharr.R

object NotificationChannels {
    const val ACTIVE = "active"
    const val RESULTS = "results"

    /** Idempotent. minSdk is 29, so channels always exist. */
    fun create(context: Context) {
        val manager = context.getSystemService(NotificationManager::class.java) ?: return
        val active = NotificationChannel(
            ACTIVE,
            context.getString(R.string.channel_active_name),
            NotificationManager.IMPORTANCE_LOW,
        ).apply {
            description = context.getString(R.string.channel_active_description)
            setSound(null, null)
            enableVibration(false)
            setShowBadge(false)
        }
        val results = NotificationChannel(
            RESULTS,
            context.getString(R.string.channel_results_name),
            NotificationManager.IMPORTANCE_DEFAULT,
        ).apply {
            description = context.getString(R.string.channel_results_description)
        }
        manager.createNotificationChannels(listOf(active, results))
    }
}
