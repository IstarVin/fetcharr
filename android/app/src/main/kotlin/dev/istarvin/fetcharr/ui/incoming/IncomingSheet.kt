package dev.istarvin.fetcharr.ui.incoming

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TriStateCheckbox
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.state.ToggleableState
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.istarvin.fetcharr.R
import dev.istarvin.fetcharr.incoming.IncomingSheet
import dev.istarvin.fetcharr.incoming.SeasonEpisode
import java.util.Locale

/**
 * The Incoming sheet (spec 5.1), shown over any destination while the coordinator has a sheet.
 *
 * "Server not set up" flow: with no server saved, the sheet shows a notice and an "Open Settings" button; the
 * request stays in the coordinator. While the user is on Settings with no server ([hiddenForSettings]) the sheet
 * is not composed, so they can fill the form in; as soon as they save a server, [IncomingUiState.serverConfigured]
 * flips and the sheet shows again (the form becomes usable). Going back to another tab also shows it.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun IncomingSheetHost(
    onOpenSettings: () -> Unit,
    onSettingsDestination: Boolean,
    viewModel: IncomingViewModel = hiltViewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    val sheet = state.sheet ?: return
    if (!state.settingsLoaded) return
    val hiddenForSettings = onSettingsDestination && !state.serverConfigured
    if (hiddenForSettings) return

    ModalBottomSheet(
        onDismissRequest = viewModel::cancel,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
    ) {
        Column(Modifier.fillMaxWidth().imePadding()) {
            Column(
                Modifier
                    .weight(1f, fill = false)
                    .verticalScroll(rememberScrollState())
                    .padding(horizontal = 16.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                if (!state.serverConfigured) {
                    NoServerNotice(onOpenSettings)
                } else {
                    SheetContent(state, sheet, viewModel)
                }
                if (state.waitingCount > 0) {
                    Text(
                        pluralStringResource(R.plurals.incoming_waiting, state.waitingCount, state.waitingCount),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                state.error?.let {
                    Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium)
                }
            }
            Row(
                Modifier.fillMaxWidth().padding(16.dp),
                horizontalArrangement = Arrangement.spacedBy(8.dp, Alignment.End),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                if (state.submitting) CircularProgressIndicator(Modifier.size(24.dp), strokeWidth = 2.dp)
                OutlinedButton(onClick = viewModel::cancel) { Text(stringResource(R.string.incoming_cancel)) }
                if (state.serverConfigured) {
                    Button(onClick = viewModel::download, enabled = state.canDownload) {
                        Text(stringResource(R.string.incoming_download, state.selectedCount))
                    }
                }
            }
        }
    }
}

@Composable
private fun NoServerNotice(onOpenSettings: () -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(stringResource(R.string.incoming_no_server_title), style = MaterialTheme.typography.titleMedium)
        Text(
            stringResource(R.string.incoming_no_server_body),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Button(onClick = onOpenSettings) { Text(stringResource(R.string.incoming_open_settings)) }
    }
}

@Composable
private fun SheetContent(state: IncomingUiState, sheet: IncomingSheet, vm: IncomingViewModel) {
    val actions = ItemFormActions(vm::onTitle, vm::onYear, vm::onShow, vm::onSeason, vm::onEpisode, vm::onEpisodeTitle)
    Text(stringResource(R.string.incoming_title), style = MaterialTheme.typography.titleLarge)
    CategoryChips(state.categories, state.categoryId, vm::selectCategory)
    if (state.receiving) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
            Text(stringResource(R.string.incoming_receiving), style = MaterialTheme.typography.bodyMedium)
        }
    }
    if (state.isSeasonMode && sheet is IncomingSheet.SeasonSheet) {
        SeasonFormFields(state.form, actions)
        EpisodeChecklist(sheet.episodes, vm::toggleEpisode, vm::setAllSelected)
    } else {
        state.layout?.let { ItemFormFields(it, state.form, actions) }
        state.sourceLine?.let {
            Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
private fun EpisodeChecklist(
    episodes: List<SeasonEpisode>,
    onToggle: (Int) -> Unit,
    onSetAll: (Boolean) -> Unit,
) {
    val selected = episodes.count { it.selected }
    val allSelected = selected == episodes.size
    Column {
        Row(
            Modifier.fillMaxWidth().clickable { onSetAll(!allSelected) },
            verticalAlignment = Alignment.CenterVertically,
        ) {
            TriStateCheckbox(
                state = when (selected) {
                    0 -> ToggleableState.Off
                    episodes.size -> ToggleableState.On
                    else -> ToggleableState.Indeterminate
                },
                onClick = { onSetAll(!allSelected) },
            )
            Text(stringResource(R.string.incoming_select_all, selected, episodes.size), style = MaterialTheme.typography.titleSmall)
        }
        HorizontalDivider()
        episodes.forEach { episode ->
            Row(
                Modifier.fillMaxWidth().clickable { onToggle(episode.episode) },
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Checkbox(checked = episode.selected, onCheckedChange = { onToggle(episode.episode) })
                Text(
                    "E%02d".format(Locale.ROOT, episode.episode),
                    style = MaterialTheme.typography.labelLarge,
                    modifier = Modifier.padding(end = 12.dp),
                )
                Text(episode.title.orEmpty(), style = MaterialTheme.typography.bodyLarge, modifier = Modifier.weight(1f))
            }
        }
    }
}
