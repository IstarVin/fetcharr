package dev.istarvin.fetcharr.ui.queue

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.CloudOff
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import dev.istarvin.fetcharr.R
import dev.istarvin.fetcharr.data.settings.SettingsRepository
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import javax.inject.Inject

@HiltViewModel
class QueueViewModel @Inject constructor(settings: SettingsRepository) : ViewModel() {
    /** True when a server URL is saved; null until DataStore has been read. */
    val hasServer: StateFlow<Boolean?> = settings.settings
        .map { it.serverUrl.isNotBlank() }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), null)
}

/** Placeholder; the live job list arrives in a later phase. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun QueueScreen(onOpenSettings: () -> Unit, viewModel: QueueViewModel = hiltViewModel()) {
    val hasServer by viewModel.hasServer.collectAsStateWithLifecycle()
    Scaffold(topBar = { TopAppBar(title = { Text(stringResource(R.string.nav_queue)) }) }) { padding ->
        Column(
            modifier = Modifier.fillMaxSize().padding(padding).padding(horizontal = 32.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp, Alignment.CenterVertically),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            when (hasServer) {
                null -> Unit
                false -> {
                    EmptyState(
                        icon = Icons.Outlined.CloudOff,
                        title = R.string.queue_setup_title,
                        body = R.string.queue_setup_body,
                    )
                    Button(onClick = onOpenSettings) { Text(stringResource(R.string.queue_setup_action)) }
                }
                true -> EmptyState(
                    icon = Icons.Outlined.Download,
                    title = R.string.queue_empty_title,
                    body = R.string.queue_empty_body,
                )
            }
        }
    }
}

@Composable
private fun EmptyState(icon: ImageVector, title: Int, body: Int) {
    Icon(icon, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
    Text(stringResource(title), style = MaterialTheme.typography.titleMedium)
    Text(
        stringResource(body),
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        textAlign = TextAlign.Center,
    )
}
