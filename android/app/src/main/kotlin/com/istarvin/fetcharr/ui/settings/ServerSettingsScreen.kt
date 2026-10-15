package com.istarvin.fetcharr.ui.settings

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExposedDropdownMenuAnchorType
import androidx.compose.material3.ExposedDropdownMenuBox
import androidx.compose.material3.ExposedDropdownMenuDefaults
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.istarvin.fetcharr.R
import com.istarvin.fetcharr.ui.LocalSnackbarHostState

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ServerSettingsScreen(onBack: () -> Unit, viewModel: ServerSettingsViewModel = hiltViewModel()) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    val snackbar = LocalSnackbarHostState.current
    val focusManager = LocalFocusManager.current
    val savedMessage = stringResource(R.string.server_settings_saved)
    val checkFieldsMessage = stringResource(R.string.server_settings_check_fields)
    LaunchedEffect(state.saved, state.error, state.fieldErrors) {
        val message = when {
            state.error != null -> state.error
            state.fieldErrors.isNotEmpty() -> checkFieldsMessage
            state.saved -> savedMessage
            else -> null
        }
        if (message != null) snackbar.showSnackbar(message)
    }
    var discardAction by remember(state.serverUrl) { mutableStateOf<String?>(null) }
    val leave = {
        if (!state.saving) {
            if (state.dirty) discardAction = "back" else onBack()
        }
    }
    BackHandler(enabled = state.dirty || state.saving) { leave() }

    if (discardAction != null) {
        AlertDialog(
            onDismissRequest = { discardAction = null },
            title = { Text(stringResource(R.string.server_settings_discard_title)) },
            text = { Text(stringResource(R.string.server_settings_discard_message)) },
            confirmButton = {
                TextButton(onClick = {
                    val action = discardAction
                    discardAction = null
                    if (action == "reload") viewModel.reload() else onBack()
                }) { Text(stringResource(R.string.server_settings_discard)) }
            },
            dismissButton = {
                TextButton(onClick = { discardAction = null }) { Text(stringResource(R.string.server_settings_keep_editing)) }
            },
        )
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.server_settings_title)) },
                navigationIcon = {
                    IconButton(onClick = leave, enabled = !state.saving) {
                        Icon(Icons.AutoMirrored.Outlined.ArrowBack, stringResource(R.string.server_settings_back))
                    }
                },
                actions = {
                    TextButton(onClick = { focusManager.clearFocus(); viewModel.save() }, enabled = state.dirty && !state.saving && !state.conflict) {
                        Text(stringResource(if (state.saving) R.string.server_settings_saving else R.string.settings_save))
                    }
                },
            )
        },
    ) { padding ->
        Column(
            modifier = Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState())
                .padding(horizontal = 16.dp, vertical = 8.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            state.serverUrl?.let { Text(it, style = MaterialTheme.typography.bodyMedium) }
            when {
                state.serverUrl == null -> Text(stringResource(R.string.server_settings_connect_first))
                state.loading -> CircularProgressIndicator()
                state.unsupported -> Text(stringResource(R.string.server_settings_unsupported))
                else -> {
                    state.error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
                    if (state.fieldErrors.isNotEmpty()) {
                        Text(stringResource(R.string.server_settings_check_fields), color = MaterialTheme.colorScheme.error)
                    }
                    TextButton(
                        onClick = { if (state.dirty) discardAction = "reload" else viewModel.reload() },
                        enabled = !state.saving,
                    ) { Text(stringResource(R.string.server_settings_reload)) }
                    state.draft?.let { draft -> ServerSettingsForm(state, draft, viewModel::edit) }
                }
            }
        }
    }
}

@Composable
private fun ServerSettingsForm(
    state: ServerSettingsUiState,
    draft: ServerSettingsDraft,
    edit: ((ServerSettingsDraft) -> ServerSettingsDraft) -> Unit,
) {
    val enabled = !state.saving
    SectionTitle(R.string.server_settings_downloads)
    SettingField(R.string.server_settings_concurrency, draft.concurrency, { value -> edit { it.copy(concurrency = value) } },
        enabled, state.fieldErrors["concurrency"], KeyboardType.Number)
    SettingField(R.string.server_settings_merge_format, draft.mergeFormat, { value -> edit { it.copy(mergeFormat = value) } },
        enabled, state.fieldErrors["mergeFormat"])
    SettingField(R.string.server_settings_free_space, draft.minFreeGb, { value -> edit { it.copy(minFreeGb = value) } },
        enabled, state.fieldErrors["minFreeGb"], KeyboardType.Decimal)
    Hint(R.string.server_settings_free_space_hint)

    SectionTitle(R.string.server_settings_maintenance)
    SettingField(R.string.server_settings_retention, draft.retentionDays, { value -> edit { it.copy(retentionDays = value) } },
        enabled, state.fieldErrors["retentionDays"], KeyboardType.Number)
    Hint(R.string.server_settings_retention_hint)
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
        val autoUpdateLabel = stringResource(R.string.server_settings_auto_update)
        Text(autoUpdateLabel, modifier = Modifier.weight(1f).padding(top = 12.dp))
        Switch(checked = draft.autoUpdate, onCheckedChange = { value -> edit { it.copy(autoUpdate = value) } },
            enabled = enabled && state.managedYtdlp, modifier = Modifier.semantics { contentDescription = autoUpdateLabel })
    }
    if (!state.managedYtdlp) Hint(R.string.server_settings_unmanaged_hint)

    SectionTitle(R.string.server_settings_categories)
    Hint(R.string.server_settings_paths_hint)
    if (draft.categories.isEmpty()) Hint(R.string.server_settings_no_categories)
    draft.categories.forEachIndexed { index, category ->
        key(category.key) {
            CategoryEditor(
                category, index, state,
                change = { value -> edit { d -> d.copy(categories = d.categories.map { if (it.key == category.key) value else it }) } },
                remove = { edit { d -> d.copy(categories = d.categories.filterNot { it.key == category.key }) } },
            )
        }
    }
    TextButton(onClick = { edit { it.copy(categories = it.categories + CategoryDraft()) } }, enabled = enabled) {
        Text(stringResource(R.string.server_settings_add_category))
    }

    SectionTitle(R.string.server_settings_integrations)
    if (draft.integrations.isEmpty()) Hint(R.string.server_settings_no_integrations)
    draft.integrations.forEachIndexed { index, integration ->
        key(integration.id) {
            IntegrationEditor(
                integration, index, state,
                change = { value -> edit { d -> d.copy(integrations = d.integrations.map { if (it.id == integration.id) value else it }) } },
                remove = { edit { d -> d.copy(integrations = d.integrations.filterNot { it.id == integration.id }) } },
            )
        }
    }
    TextButton(onClick = { edit { it.copy(integrations = it.integrations + IntegrationDraft()) } }, enabled = enabled) {
        Text(stringResource(R.string.server_settings_add_integration))
    }
}

@Composable
private fun CategoryEditor(c: CategoryDraft, index: Int, state: ServerSettingsUiState, change: (CategoryDraft) -> Unit, remove: () -> Unit) {
    var expanded by remember { mutableStateOf(!c.existing) }
    val errors = state.fieldErrors
    val hasErrors = errors.keys.any { it.startsWith("category.$index.") }
    HorizontalDivider()
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
        Column(Modifier.weight(1f).padding(top = 12.dp)) {
            Text(c.label.ifBlank { c.id }.ifBlank { stringResource(R.string.server_settings_new_category) }, style = MaterialTheme.typography.titleMedium)
            if (c.path.isNotBlank()) Text(c.path, style = MaterialTheme.typography.bodySmall)
        }
        TextButton(onClick = { expanded = !expanded }) {
            Text(stringResource(if (expanded) R.string.server_settings_collapse else R.string.server_settings_edit))
        }
    }
    if (expanded || hasErrors) {
        SettingField(R.string.server_settings_category_id, c.id, { change(c.copy(id = it)) }, !state.saving && !c.existing,
            errors["category.$index.id"])
        if (c.existing) Hint(R.string.server_settings_category_id_hint)
        SettingField(R.string.server_settings_label, c.label, { change(c.copy(label = it)) }, !state.saving)
        SettingField(R.string.server_settings_destination, c.path, { change(c.copy(path = it)) }, !state.saving,
            errors["category.$index.path"])
        ChoiceField(R.string.server_settings_layout, c.layout, listOf("movie" to R.string.server_settings_movie,
            "tv" to R.string.server_settings_tv, "flat" to R.string.server_settings_flat), !state.saving) { change(c.copy(layout = it)) }
        TextButton(onClick = remove, enabled = !state.saving) { Text(stringResource(R.string.server_settings_remove_category), color = MaterialTheme.colorScheme.error) }
    }
}

@Composable
private fun IntegrationEditor(s: IntegrationDraft, index: Int, state: ServerSettingsUiState, change: (IntegrationDraft) -> Unit, remove: () -> Unit) {
    var expanded by remember { mutableStateOf(!s.existing) }
    val errors = state.fieldErrors
    val hasErrors = errors.keys.any { it.startsWith("integration.$index.") }
    HorizontalDivider()
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
        Column(Modifier.weight(1f).padding(top = 12.dp)) {
            Text(stringResource(if (s.type == "plex") R.string.server_settings_plex else R.string.server_settings_jellyfin), style = MaterialTheme.typography.titleMedium)
            if (s.url.isNotBlank()) Text(s.url, style = MaterialTheme.typography.bodySmall)
        }
        TextButton(onClick = { expanded = !expanded }) {
            Text(stringResource(if (expanded) R.string.server_settings_collapse else R.string.server_settings_edit))
        }
    }
    if (expanded || hasErrors) {
        ChoiceField(R.string.server_settings_type, s.type, listOf("jellyfin" to R.string.server_settings_jellyfin,
            "plex" to R.string.server_settings_plex), !state.saving && !s.existing) { change(s.copy(type = it)) }
        SettingField(R.string.server_settings_integration_url, s.url, { change(s.copy(url = it)) }, !state.saving,
            errors["integration.$index.url"], KeyboardType.Uri)
        SettingField(if (s.type == "plex") R.string.server_settings_plex_token else R.string.server_settings_api_key,
            s.credential, { change(s.copy(credential = it)) }, !state.saving, errors["integration.$index.credential"], KeyboardType.Password, secret = true)
        if (s.hasCredential) Hint(R.string.server_settings_secret_hint)
        Text(stringResource(R.string.server_settings_path_maps), style = MaterialTheme.typography.titleSmall)
        Hint(R.string.server_settings_mapping_hint)
        s.paths.forEachIndexed { pathIndex, p ->
            key(p.key) {
                SettingField(R.string.server_settings_mapping_from, p.from, { value ->
                    change(s.copy(paths = s.paths.map { if (it.key == p.key) it.copy(from = value) else it }))
                }, !state.saving, errors["integration.$index.path.$pathIndex"])
                SettingField(R.string.server_settings_mapping_to, p.to, { value ->
                    change(s.copy(paths = s.paths.map { if (it.key == p.key) it.copy(to = value) else it }))
                }, !state.saving)
                TextButton(onClick = { change(s.copy(paths = s.paths.filterNot { it.key == p.key })) }, enabled = !state.saving) {
                    Text(stringResource(R.string.server_settings_remove_mapping))
                }
            }
        }
        TextButton(onClick = { change(s.copy(paths = s.paths + PathMappingDraft())) }, enabled = !state.saving) {
            Text(stringResource(R.string.server_settings_add_mapping))
        }
        TextButton(onClick = remove, enabled = !state.saving) { Text(stringResource(R.string.server_settings_remove_integration), color = MaterialTheme.colorScheme.error) }
    }
}

@Composable
private fun SettingField(
    label: Int, value: String, onChange: (String) -> Unit, enabled: Boolean, error: Int? = null,
    keyboard: KeyboardType = KeyboardType.Text, secret: Boolean = false,
) {
    OutlinedTextField(
        value = value, onValueChange = onChange, label = { Text(stringResource(label)) }, modifier = Modifier.fillMaxWidth(),
        enabled = enabled, singleLine = true, isError = error != null,
        supportingText = error?.let { { Text(stringResource(it)) } },
        keyboardOptions = KeyboardOptions(keyboardType = keyboard),
        visualTransformation = if (secret) PasswordVisualTransformation() else VisualTransformation.None,
    )
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ChoiceField(label: Int, selected: String, options: List<Pair<String, Int>>, enabled: Boolean, onSelect: (String) -> Unit) {
    var expanded by remember { mutableStateOf(false) }
    ExposedDropdownMenuBox(expanded = expanded, onExpandedChange = { if (enabled) expanded = it }) {
        OutlinedTextField(
            value = options.firstOrNull { it.first == selected }?.let { stringResource(it.second) } ?: selected,
            onValueChange = {}, readOnly = true, enabled = enabled,
            label = { Text(stringResource(label)) }, trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded) },
            modifier = Modifier.fillMaxWidth().menuAnchor(ExposedDropdownMenuAnchorType.PrimaryNotEditable, enabled),
        )
        ExposedDropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            options.forEach { (value, title) ->
                DropdownMenuItem(text = { Text(stringResource(title)) }, onClick = { onSelect(value); expanded = false })
            }
        }
    }
}

@Composable
private fun SectionTitle(title: Int) {
    Text(stringResource(title), style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(top = 16.dp))
}

@Composable
private fun Hint(text: Int) {
    Text(stringResource(text), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
}
