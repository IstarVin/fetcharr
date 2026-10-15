package com.istarvin.fetcharr.ui.incoming

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.selection.triStateToggleable
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
import androidx.compose.material3.TextButton
import androidx.compose.material3.TriStateCheckbox
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.state.ToggleableState
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.istarvin.fetcharr.ui.common.screenGutter
import com.istarvin.fetcharr.R
import com.istarvin.fetcharr.data.network.ApiError
import com.istarvin.fetcharr.incoming.IncomingSheet
import com.istarvin.fetcharr.incoming.SeasonEpisode
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
        sheetMaxWidth = 720.dp,
    ) {
        Column(Modifier.fillMaxWidth()) {
            Column(
                Modifier
                    .weight(1f, fill = false)
                    .verticalScroll(rememberScrollState())
                    .padding(horizontal = screenGutter(), vertical = 8.dp),
                verticalArrangement = Arrangement.spacedBy(24.dp),
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
                    if (it == ApiError.Unauthorized.userMessage) {
                        TextButton(onClick = onOpenSettings, modifier = Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.queue_snackbar_settings)) }
                    }
                }
            }
            FormFooter {
                if (state.submitting) {
                    val sending = stringResource(R.string.a11y_sending)
                    CircularProgressIndicator(Modifier.size(24.dp).semantics { contentDescription = sending }, strokeWidth = 2.dp)
                }
                OutlinedButton(onClick = viewModel::cancel, modifier = Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.incoming_cancel)) }
                if (state.serverConfigured) {
                    Button(onClick = viewModel::download, enabled = state.canDownload, modifier = Modifier.heightIn(min = 48.dp)) {
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
        Button(onClick = onOpenSettings, modifier = Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.incoming_open_settings)) }
    }
}

@Composable
private fun SheetContent(state: IncomingUiState, sheet: IncomingSheet, vm: IncomingViewModel) {
    val actions = ItemFormActions(vm::onTitle, vm::onYear, vm::onShow, vm::onSeason, vm::onEpisode, vm::onEpisodeTitle)
    Text(stringResource(R.string.incoming_title), style = MaterialTheme.typography.titleLarge)
    FormSection(stringResource(R.string.field_category)) {
        CategoryChips(state.categories, state.categoryId, vm::selectCategory)
    }
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
internal fun EpisodeChecklist(
    episodes: List<SeasonEpisode>,
    onToggle: (Int) -> Unit,
    onSetAll: (Boolean) -> Unit,
) {
    val selected = episodes.count { it.selected }
    val allSelected = selected == episodes.size
    Column {
        Row(
            Modifier.fillMaxWidth().heightIn(min = 56.dp).triStateToggleable(
                state = when (selected) {
                    0 -> ToggleableState.Off
                    episodes.size -> ToggleableState.On
                    else -> ToggleableState.Indeterminate
                },
                role = Role.Checkbox,
                onClick = { onSetAll(!allSelected) },
            ).padding(vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            TriStateCheckbox(
                state = when (selected) {
                    0 -> ToggleableState.Off
                    episodes.size -> ToggleableState.On
                    else -> ToggleableState.Indeterminate
                },
                onClick = null,
            )
            Text(
                stringResource(R.string.incoming_select_all, selected, episodes.size),
                style = MaterialTheme.typography.titleSmall,
                modifier = Modifier.weight(1f).padding(start = 12.dp),
            )
        }
        HorizontalDivider()
        episodes.forEach { episode ->
            Row(
                Modifier.fillMaxWidth().heightIn(min = 56.dp).toggleable(
                    value = episode.selected,
                    role = Role.Checkbox,
                    onValueChange = { onToggle(episode.episode) },
                ).padding(vertical = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Checkbox(checked = episode.selected, onCheckedChange = null)
                Text(
                    "E%02d".format(Locale.ROOT, episode.episode),
                    style = MaterialTheme.typography.labelLarge,
                    modifier = Modifier.padding(horizontal = 12.dp),
                )
                Text(episode.title.orEmpty(), style = MaterialTheme.typography.bodyLarge, modifier = Modifier.weight(1f))
            }
        }
    }
}
