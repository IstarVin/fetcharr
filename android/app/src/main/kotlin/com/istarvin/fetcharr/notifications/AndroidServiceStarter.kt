package com.istarvin.fetcharr.notifications

import android.content.Context
import android.content.Intent
import androidx.core.content.ContextCompat
import dagger.hilt.android.qualifiers.ApplicationContext
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class AndroidServiceStarter @Inject constructor(
    @ApplicationContext private val context: Context,
) : ServiceStarter {

    override fun start(): Boolean =
        try {
            ContextCompat.startForegroundService(context, Intent(context, SyncForegroundService::class.java))
            true
        } catch (e: IllegalStateException) {
            // ForegroundServiceStartNotAllowedException (API 31+) is a subclass: the app is in the background
            // without an exemption. Skip; the next event tries again.
            false
        } catch (e: SecurityException) {
            false
        }
}
