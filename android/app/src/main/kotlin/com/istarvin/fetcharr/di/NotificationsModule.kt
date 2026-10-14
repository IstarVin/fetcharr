package com.istarvin.fetcharr.di

import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import com.istarvin.fetcharr.notifications.AndroidServiceStarter
import com.istarvin.fetcharr.notifications.ApiJobsGateway
import com.istarvin.fetcharr.notifications.BackgroundController
import com.istarvin.fetcharr.notifications.DataStoreNotifierStateStore
import com.istarvin.fetcharr.notifications.JobsGateway
import com.istarvin.fetcharr.notifications.LastSeenStore
import com.istarvin.fetcharr.notifications.NotifierStateStore
import com.istarvin.fetcharr.notifications.ResultPoster
import com.istarvin.fetcharr.notifications.ServiceStarter
import com.istarvin.fetcharr.notifications.SettingsLastSeenStore
import com.istarvin.fetcharr.notifications.StoppedNoticePoster
import com.istarvin.fetcharr.notifications.SubmitListener
import com.istarvin.fetcharr.notifications.SystemNotifier
import com.istarvin.fetcharr.notifications.TimeSource
import javax.inject.Singleton

@Module
@InstallIn(SingletonComponent::class)
abstract class NotificationsModule {
    @Binds
    abstract fun bindServiceStarter(impl: AndroidServiceStarter): ServiceStarter

    @Binds
    abstract fun bindStoppedNoticePoster(impl: SystemNotifier): StoppedNoticePoster

    @Binds
    abstract fun bindResultPoster(impl: SystemNotifier): ResultPoster

    @Binds
    abstract fun bindSubmitListener(impl: BackgroundController): SubmitListener

    @Binds
    abstract fun bindJobsGateway(impl: ApiJobsGateway): JobsGateway

    @Binds
    abstract fun bindNotifierStateStore(impl: DataStoreNotifierStateStore): NotifierStateStore

    @Binds
    abstract fun bindLastSeenStore(impl: SettingsLastSeenStore): LastSeenStore

    companion object {
        @Provides
        @Singleton
        fun provideTimeSource(): TimeSource = TimeSource { System.currentTimeMillis() }
    }
}
