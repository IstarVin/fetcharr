package dev.istarvin.fetcharr.di

import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import dev.istarvin.fetcharr.data.sync.CategoryConnectionRefresher
import dev.istarvin.fetcharr.data.sync.ConnectionRefresher
import dev.istarvin.fetcharr.data.sync.OkHttpWebSocketConnector
import dev.istarvin.fetcharr.data.sync.WebSocketConnector
import javax.inject.Singleton

@Module
@InstallIn(SingletonComponent::class)
abstract class SyncModule {
    @Binds
    @Singleton
    abstract fun bindWebSocketConnector(impl: OkHttpWebSocketConnector): WebSocketConnector

    @Binds
    abstract fun bindConnectionRefresher(impl: CategoryConnectionRefresher): ConnectionRefresher
}
