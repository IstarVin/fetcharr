package dev.istarvin.fetcharr.notifications

import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import dev.istarvin.fetcharr.data.settings.SettingsRepository
import dev.istarvin.fetcharr.notifications.logic.NotifierState
import kotlinx.coroutines.flow.first
import javax.inject.Inject
import javax.inject.Singleton

/**
 * [NotifierState] in the settings DataStore, so de-duplication and "this batch was summarized" survive process
 * death. Both sets keep insertion order (newest last) so the notifier can cap them.
 */
@Singleton
class DataStoreNotifierStateStore @Inject constructor(
    private val dataStore: DataStore<Preferences>,
) : NotifierStateStore {

    override suspend fun load(): NotifierState {
        val prefs = dataStore.data.first()
        return NotifierState(
            summarizedBatchIds = decode(prefs[SUMMARIZED]),
            notifiedJobKeys = decode(prefs[NOTIFIED]),
        )
    }

    override suspend fun save(state: NotifierState) {
        dataStore.edit {
            it[SUMMARIZED] = state.summarizedBatchIds.joinToString(SEPARATOR)
            it[NOTIFIED] = state.notifiedJobKeys.joinToString(SEPARATOR)
        }
    }

    private fun decode(value: String?): Set<String> =
        value?.split(SEPARATOR)?.filterTo(LinkedHashSet()) { it.isNotEmpty() } ?: emptySet()

    private companion object {
        const val SEPARATOR = "\n"
        val SUMMARIZED = stringPreferencesKey("notifier_summarized_batches")
        val NOTIFIED = stringPreferencesKey("notifier_notified_jobs")
    }
}

@Singleton
class SettingsLastSeenStore @Inject constructor(
    private val settings: SettingsRepository,
) : LastSeenStore {
    override suspend fun get(): Long? = settings.settings.first().lastSeenFinishedAt

    override suspend fun set(epochMs: Long) = settings.setLastSeenFinishedAt(epochMs)

    override suspend fun initIfAbsent(nowMs: Long) = settings.initLastSeenIfAbsent(nowMs)
}
