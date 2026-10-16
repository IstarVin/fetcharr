package com.istarvin.fetcharr.data.settings

import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.emptyPreferences
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/**
 * A [DataStore] that keeps its state in memory. Tests that only need settings to behave like a store use this;
 * the real file-backed DataStore stalled on loaded CI runners, which only showed up as 30 second timeouts.
 */
internal class InMemoryPreferencesDataStore : DataStore<Preferences> {
    private val state = MutableStateFlow(emptyPreferences())
    private val writes = Mutex()

    override val data: Flow<Preferences> = state

    override suspend fun updateData(transform: suspend (t: Preferences) -> Preferences): Preferences =
        writes.withLock {
            transform(state.value).toMutablePreferences().toPreferences().also { state.value = it }
        }
}
