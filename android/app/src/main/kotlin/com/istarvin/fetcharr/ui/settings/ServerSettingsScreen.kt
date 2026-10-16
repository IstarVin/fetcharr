package com.istarvin.fetcharr.ui.settings

import androidx.activity.compose.BackHandler
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Dns
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.ErrorOutline
import androidx.compose.material.icons.outlined.ExpandMore
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.Movie
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Save
import androidx.compose.material.icons.outlined.Schedule
import androidx.compose.material.icons.outlined.Storage
import androidx.compose.material.icons.outlined.SystemUpdateAlt
import androidx.compose.material.icons.outlined.Tv
import androidx.compose.material.icons.outlined.VideoFile
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.FilledTonalButton
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
import androidx.compose.material3.Surface
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
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.istarvin.fetcharr.R
import com.istarvin.fetcharr.ui.LocalSnackbarHostState
import com.istarvin.fetcharr.ui.common.AppCard
import com.istarvin.fetcharr.ui.common.IconTile
import com.istarvin.fetcharr.ui.common.Pill
import com.istarvin.fetcharr.ui.common.SectionLabel
import com.istarvin.fetcharr.ui.common.SettingsRow
import com.istarvin.fetcharr.ui.common.screenGutter
import com.istarvin.fetcharr.ui.common.ScreenFrame

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
    LaunchedEffect(state.saving) {
        if (!state.saving && discardAction == "saving") discardAction = null
    }
    val leave = {
        when {
            state.saving -> discardAction = "saving"
            state.dirty -> discardAction = "back"
            else -> onBack()
        }
    }
    BackHandler(enabled = state.dirty || state.saving) { leave() }

    if (discardAction != null) {
        AlertDialog(
            onDismissRequest = { discardAction = null },
            title = { Text(stringResource(if (discardAction == "saving") R.string.server_settings_leave_saving_title else R.string.server_settings_discard_title)) },
            text = { Text(stringResource(if (discardAction == "saving") R.string.server_settings_leave_saving_message else R.string.server_settings_discard_message)) },
            confirmButton = {
                TextButton(modifier = Modifier.heightIn(min = 48.dp), onClick = {
                    val action = discardAction
                    discardAction = null
                    if (action == "reload") viewModel.reload() else onBack()
                }) { Text(stringResource(if (discardAction == "saving") R.string.server_settings_leave else R.string.server_settings_discard)) }
            },
            dismissButton = {
                TextButton(modifier = Modifier.heightIn(min = 48.dp), onClick = { discardAction = null }) { Text(stringResource(R.string.server_settings_keep_editing)) }
            },
        )
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.server_settings_title)) },
                navigationIcon = {
                    IconButton(onClick = leave) {
                        Icon(Icons.AutoMirrored.Outlined.ArrowBack, stringResource(R.string.server_settings_back))
                    }
                },
                actions = {
                    if (state.serverUrl != null && !state.loading && !state.unsupported) {
                        IconButton(
                            onClick = { if (state.dirty) discardAction = "reload" else viewModel.reload() },
                            enabled = !state.saving,
                        ) { Icon(Icons.Outlined.Refresh, contentDescription = stringResource(R.string.server_settings_reload)) }
                    }
                    FilledTonalButton(
                        modifier = Modifier.heightIn(min = 40.dp).padding(end = 8.dp),
                        onClick = { focusManager.clearFocus(); viewModel.save() },
                        enabled = state.dirty && !state.saving && !state.conflict,
                    ) {
                        if (state.saving) {
                            CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
                        } else {
                            Icon(Icons.Outlined.Save, contentDescription = null, modifier = Modifier.size(18.dp))
                        }
                        Text(stringResource(if (state.saving) R.string.server_settings_saving else R.string.settings_save), Modifier.padding(start = 8.dp))
                    }
                },
            )
        },
    ) { padding ->
        val horizontalPadding = screenGutter()
        ScreenFrame(Modifier.padding(padding).imePadding()) {
            Column(
                modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState())
                    .padding(horizontal = horizontalPadding, vertical = 16.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                state.serverUrl?.let { Pill(it, icon = Icons.Outlined.Dns, modifier = Modifier.align(Alignment.Start)) }
                when {
                    state.serverUrl == null -> Hint(R.string.server_settings_connect_first)
                    state.loading -> Box(Modifier.fillMaxWidth().padding(top = 48.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
                    state.unsupported -> Hint(R.string.server_settings_unsupported)
                    else -> {
                        state.error?.let { ErrorNotice(it) }
                        if (state.fieldErrors.isNotEmpty()) ErrorNotice(stringResource(R.string.server_settings_check_fields))
                        state.draft?.let { draft -> ServerSettingsForm(state, draft, viewModel::edit) }
                    }
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
    AppCard {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            SettingField(R.string.server_settings_concurrency, draft.concurrency, { value -> edit { it.copy(concurrency = value) } },
                enabled, state.fieldErrors["concurrency"], KeyboardType.Number, icon = Icons.Outlined.Download)
            SettingField(R.string.server_settings_merge_format, draft.mergeFormat, { value -> edit { it.copy(mergeFormat = value) } },
                enabled, state.fieldErrors["mergeFormat"], icon = Icons.Outlined.VideoFile)
            SettingField(R.string.server_settings_free_space, draft.minFreeGb, { value -> edit { it.copy(minFreeGb = value) } },
                enabled, state.fieldErrors["minFreeGb"], KeyboardType.Decimal, icon = Icons.Outlined.Storage)
            Hint(R.string.server_settings_free_space_hint)
        }
    }

    SectionTitle(R.string.server_settings_maintenance)
    AppCard {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            SettingField(R.string.server_settings_retention, draft.retentionDays, { value -> edit { it.copy(retentionDays = value) } },
                enabled, state.fieldErrors["retentionDays"], KeyboardType.Number, icon = Icons.Outlined.Schedule)
            Hint(R.string.server_settings_retention_hint)
        }
        HorizontalDivider(Modifier.padding(horizontal = 16.dp), color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.5f))
        val autoUpdateLabel = stringResource(R.string.server_settings_auto_update)
        val switchEnabled = enabled && state.managedYtdlp
        SettingsRow(
            icon = Icons.Outlined.SystemUpdateAlt,
            title = autoUpdateLabel,
            modifier = Modifier.toggleable(
                value = draft.autoUpdate,
                enabled = switchEnabled,
                role = Role.Switch,
                onValueChange = { value -> edit { it.copy(autoUpdate = value) } },
            ),
            enabled = switchEnabled,
            trailing = { Switch(checked = draft.autoUpdate, onCheckedChange = null, enabled = switchEnabled) },
        )
        if (!state.managedYtdlp) {
            Box(Modifier.padding(start = 16.dp, end = 16.dp, bottom = 16.dp)) { Hint(R.string.server_settings_unmanaged_hint) }
        }
    }

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
    AddButton(R.string.server_settings_add_category, enabled) { edit { it.copy(categories = it.categories + CategoryDraft()) } }

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
    AddButton(R.string.server_settings_add_integration, enabled) { edit { it.copy(integrations = it.integrations + IntegrationDraft()) } }
    Spacer(Modifier.height(16.dp))
}

@Composable
private fun AddButton(label: Int, enabled: Boolean, onClick: () -> Unit) {
    FilledTonalButton(modifier = Modifier.heightIn(min = 48.dp), onClick = onClick, enabled = enabled) {
        Icon(Icons.Outlined.Add, contentDescription = null, modifier = Modifier.size(18.dp))
        Text(stringResource(label), Modifier.padding(start = 8.dp))
    }
}

@Composable
private fun RemoveButton(label: Int, enabled: Boolean, onClick: () -> Unit) {
    TextButton(
        modifier = Modifier.heightIn(min = 48.dp),
        onClick = onClick,
        enabled = enabled,
        colors = ButtonDefaults.textButtonColors(contentColor = MaterialTheme.colorScheme.error),
    ) {
        Icon(Icons.Outlined.Delete, contentDescription = null, modifier = Modifier.size(18.dp))
        Text(stringResource(label), Modifier.padding(start = 8.dp))
    }
}

@Composable
private fun ErrorNotice(message: String) {
    Surface(color = MaterialTheme.colorScheme.errorContainer, contentColor = MaterialTheme.colorScheme.onErrorContainer, shape = MaterialTheme.shapes.medium) {
        Row(Modifier.fillMaxWidth().padding(12.dp), horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically) {
            Icon(Icons.Outlined.ErrorOutline, contentDescription = null)
            Text(message, style = MaterialTheme.typography.bodyMedium)
        }
    }
}

/** The expand/collapse indicator shared by the clickable category and integration cards. */
@Composable
private fun ExpandIndicator(expanded: Boolean) {
    val rotation by animateFloatAsState(if (expanded) 180f else 0f, label = "expand")
    Box(Modifier.size(48.dp), contentAlignment = Alignment.Center) {
        Icon(
            Icons.Outlined.ExpandMore,
            contentDescription = stringResource(if (expanded) R.string.server_settings_collapse else R.string.server_settings_edit),
            modifier = Modifier.rotate(rotation),
        )
    }
}

@Composable
private fun CategoryEditor(c: CategoryDraft, index: Int, state: ServerSettingsUiState, change: (CategoryDraft) -> Unit, remove: () -> Unit) {
    var expanded by remember { mutableStateOf(!c.existing) }
    val errors = state.fieldErrors
    val hasErrors = errors.keys.any { it.startsWith("category.$index.") }
    val icon = when (c.layout) {
        "movie" -> Icons.Outlined.Movie
        "tv" -> Icons.Outlined.Tv
        else -> Icons.Outlined.Folder
    }
    AppCard(onClick = { expanded = !expanded }) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(14.dp), verticalAlignment = Alignment.CenterVertically) {
                IconTile(icon)
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                    Text(c.label.ifBlank { c.id }.ifBlank { stringResource(R.string.server_settings_new_category) }, style = MaterialTheme.typography.titleMedium)
                    if (c.path.isNotBlank()) Text(c.path, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                ExpandIndicator(expanded || hasErrors)
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
                RemoveButton(R.string.server_settings_remove_category, !state.saving, remove)
            }
        }
    }
}

@Composable
private fun IntegrationEditor(s: IntegrationDraft, index: Int, state: ServerSettingsUiState, change: (IntegrationDraft) -> Unit, remove: () -> Unit) {
    var expanded by remember { mutableStateOf(!s.existing) }
    val errors = state.fieldErrors
    val hasErrors = errors.keys.any { it.startsWith("integration.$index.") }
    AppCard(onClick = { expanded = !expanded }) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(14.dp), verticalAlignment = Alignment.CenterVertically) {
                IconTile(
                    Icons.Outlined.Dns,
                    container = MaterialTheme.colorScheme.tertiaryContainer,
                    content = MaterialTheme.colorScheme.onTertiaryContainer,
                )
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                    Text(stringResource(if (s.type == "plex") R.string.server_settings_plex else R.string.server_settings_jellyfin), style = MaterialTheme.typography.titleMedium)
                    if (s.url.isNotBlank()) Text(s.url, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                ExpandIndicator(expanded || hasErrors)
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
                        HorizontalDivider()
                        SettingField(R.string.server_settings_mapping_from, p.from, { value ->
                            change(s.copy(paths = s.paths.map { if (it.key == p.key) it.copy(from = value) else it }))
                        }, !state.saving, errors["integration.$index.path.$pathIndex"])
                        SettingField(R.string.server_settings_mapping_to, p.to, { value ->
                            change(s.copy(paths = s.paths.map { if (it.key == p.key) it.copy(to = value) else it }))
                        }, !state.saving)
                        RemoveButton(R.string.server_settings_remove_mapping, !state.saving) { change(s.copy(paths = s.paths.filterNot { it.key == p.key })) }
                    }
                }
                AddButton(R.string.server_settings_add_mapping, !state.saving) { change(s.copy(paths = s.paths + PathMappingDraft())) }
                RemoveButton(R.string.server_settings_remove_integration, !state.saving, remove)
            }
        }
    }
}

@Composable
private fun SettingField(
    label: Int, value: String, onChange: (String) -> Unit, enabled: Boolean, error: Int? = null,
    keyboard: KeyboardType = KeyboardType.Text, secret: Boolean = false, icon: ImageVector? = null,
) {
    OutlinedTextField(
        value = value, onValueChange = onChange, label = { Text(stringResource(label)) }, modifier = Modifier.fillMaxWidth(),
        leadingIcon = icon?.let { { Icon(it, contentDescription = null) } },
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
    SectionLabel(stringResource(title), Modifier.padding(top = 8.dp))
}

@Composable
private fun Hint(text: Int) {
    Text(stringResource(text), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(horizontal = 4.dp))
}
