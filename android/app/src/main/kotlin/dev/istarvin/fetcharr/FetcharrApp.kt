package dev.istarvin.fetcharr

import android.app.Application
import dagger.hilt.android.HiltAndroidApp
import dev.istarvin.fetcharr.notifications.BackgroundController
import dev.istarvin.fetcharr.notifications.NotificationChannels
import dev.istarvin.fetcharr.notifications.ResultNotifier
import javax.inject.Inject

@HiltAndroidApp
class FetcharrApp : Application() {
    @Inject lateinit var backgroundController: BackgroundController

    @Inject lateinit var resultNotifier: ResultNotifier

    override fun onCreate() {
        super.onCreate()
        NotificationChannels.create(this)
        backgroundController.start()
        resultNotifier.start()
    }
}
