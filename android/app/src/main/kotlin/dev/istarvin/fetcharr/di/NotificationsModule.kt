package dev.istarvin.fetcharr.di

import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import dev.istarvin.fetcharr.notifications.AndroidServiceStarter
import dev.istarvin.fetcharr.notifications.ApiJobsGateway
import dev.istarvin.fetcharr.notifications.BackgroundController
import dev.istarvin.fetcharr.notifications.DataStoreNotifierStateStore
import dev.istarvin.fetcharr.notifications.JobsGateway
import dev.istarvin.fetcharr.notifications.LastSeenStore
import dev.istarvin.fetcharr.notifications.NotifierStateStore
import dev.istarvin.fetcharr.notifications.ResultPoster
import dev.istarvin.fetcharr.notifications.ServiceStarter
import dev.istarvin.fetcharr.notifications.SettingsLastSeenStore
import dev.istarvin.fetcharr.notifications.StoppedNoticePoster
import dev.istarvin.fetcharr.notifications.SubmitListener
import dev.istarvin.fetcharr.notifications.SystemNotifier
import dev.istarvin.fetcharr.notifications.TimeSource
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
