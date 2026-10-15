package com.istarvin.fetcharr

import android.app.Application
import dagger.hilt.android.HiltAndroidApp
import com.istarvin.fetcharr.notifications.BackgroundController
import com.istarvin.fetcharr.notifications.NotificationChannels
import com.istarvin.fetcharr.notifications.ResultNotifier
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
