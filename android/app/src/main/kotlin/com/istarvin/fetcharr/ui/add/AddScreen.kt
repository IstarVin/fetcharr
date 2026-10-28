package com.istarvin.fetcharr.ui.add

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.CloudOff
import androidx.compose.material.icons.outlined.Code
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.Link
import androidx.compose.material.icons.outlined.Subtitles
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.AssistChip
import androidx.compose.material3.AssistChipDefaults
import androidx.compose.material3.Button
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.material.icons.outlined.ContentPaste
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material.icons.outlined.ExpandMore
import androidx.compose.material.icons.outlined.Tune
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Surface
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.res.pluralStringResource
import kotlinx.coroutines.launch
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.DialogProperties
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.istarvin.fetcharr.ui.common.screenGutter
import com.istarvin.fetcharr.R
import com.istarvin.fetcharr.ui.common.AppCard
import com.istarvin.fetcharr.ui.common.EmptyState
import com.istarvin.fetcharr.ui.common.LoadingBlock
import com.istarvin.fetcharr.data.network.ApiError
import com.istarvin.fetcharr.ui.common.ScreenFrame
import com.istarvin.fetcharr.ui.incoming.FormFooter
import com.istarvin.fetcharr.ui.incoming.FormSection
import com.istarvin.fetcharr.ui.incoming.ResponsiveFormRow
import com.istarvin.fetcharr.ui.incoming.CategoryChips
import com.istarvin.fetcharr.ui.incoming.ItemFormActions
import com.istarvin.fetcharr.ui.incoming.ItemFormFields
import com.istarvin.fetcharr.ui.incoming.ReplaceExistingDialog
import com.istarvin.fetcharr.ui.incoming.isHttpUrl
import com.istarvin.fetcharr.ui.incoming.layoutIcon
import com.istarvin.fetcharr.incoming.CategoryOption

/** Manual add (spec 5.2). The URL is pre-filled from the share sheet via the route's `url` argument. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AddScreen(
    onBack: () -> Unit,
    onOpenSettings: () -> Unit,
    onOpenServerSettings: () -> Unit = onOpenSettings,
    viewModel: AddViewModel = hiltViewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    LaunchedEffect(viewModel) {
        viewModel.events.collect { if (it == AddEvent.Done) onBack() }
    }
    val actions = ItemFormActions(
        viewModel::onTitle, viewModel::onYear, viewModel::onShow,
        viewModel::onSeason, viewModel::onEpisode, viewModel::onEpisodeTitle,
    )

    AddContent(
        state = state,
        onBack = onBack,
        onOpenSettings = onOpenSettings,
        onUrl = viewModel::onUrl,
        onSelectCategory = viewModel::selectCategory,
        actions = actions,
        onHeaderName = { id, value -> viewModel.updateHeader(id, name = value) },
        onHeaderValue = { id, value -> viewModel.updateHeader(id, value = value) },
        onRemoveHeader = viewModel::removeHeader,
        onAddHeader = viewModel::addHeader,
        onSubtitleUrl = { id, value -> viewModel.updateSubtitle(id, url = value) },
        onSubtitleLanguage = { id, value -> viewModel.updateSubtitle(id, lang = value) },
        onRemoveSubtitle = viewModel::removeSubtitle,
        onAddSubtitle = viewModel::addSubtitle,
        onSubmit = viewModel::submit,
        onRefreshCategories = viewModel::refreshCategories,
        onOpenServerSettings = onOpenServerSettings,
        onAddCategory = viewModel::openNewCategory,
        onEditNewCategory = viewModel::editNewCategory,
        onDismissNewCategory = viewModel::dismissNewCategory,
        onSaveNewCategory = viewModel::saveNewCategory,
        onReplaceExisting = viewModel::replaceExisting,
        onKeepExisting = viewModel::keepExisting,
    )
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun AddContent(
    state: AddUiState,
    onBack: () -> Unit,
    onOpenSettings: () -> Unit,
    onUrl: (String) -> Unit,
    onSelectCategory: (String) -> Unit,
    actions: ItemFormActions,
    onHeaderName: (Int, String) -> Unit,
    onHeaderValue: (Int, String) -> Unit,
    onRemoveHeader: (Int) -> Unit,
    onAddHeader: () -> Unit,
    onSubtitleUrl: (Int, String) -> Unit,
    onSubtitleLanguage: (Int, String) -> Unit,
    onRemoveSubtitle: (Int) -> Unit,
    onAddSubtitle: () -> Unit,
    onSubmit: () -> Unit,
    onRefreshCategories: () -> Unit = {},
    onOpenServerSettings: () -> Unit = onOpenSettings,
    onAddCategory: () -> Unit = {},
    onEditNewCategory: ((NewCategoryDraft) -> NewCategoryDraft) -> Unit = {},
    onDismissNewCategory: () -> Unit = {},
    onSaveNewCategory: () -> Unit = {},
    onReplaceExisting: () -> Unit = {},
    onKeepExisting: () -> Unit = {},
) {
    val clipboard = LocalClipboard.current
    val scope = rememberCoroutineScope()
    state.newCategory?.let { draft ->
        NewCategoryDialog(draft, onEditNewCategory, onDismissNewCategory, onSaveNewCategory)
    }
    if (state.confirmReplace) ReplaceExistingDialog(season = false, onReplaceExisting, onKeepExisting, onKeepExisting)
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.add_title)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Outlined.ArrowBack, contentDescription = stringResource(R.string.action_back))
                    }
                },
            )
        },
        bottomBar = {
            if (state.settingsLoaded && state.serverConfigured) {
                FormFooter(Modifier.imePadding().navigationBarsPadding()) {
                    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        state.error?.let { error ->
                            Text(
                                error,
                                color = MaterialTheme.colorScheme.error,
                                style = MaterialTheme.typography.bodyMedium,
                                modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                            )
                            if (error == ApiError.Unauthorized.userMessage) {
                                TextButton(onClick = onOpenSettings, modifier = Modifier.heightIn(min = 48.dp)) {
                                    Text(stringResource(R.string.queue_snackbar_settings))
                                }
                            }
                        }
                        Row(
                            Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.spacedBy(12.dp, Alignment.End),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            if (state.submitting) {
                                val sending = stringResource(R.string.a11y_sending)
                                CircularProgressIndicator(
                                    Modifier.size(24.dp).semantics { contentDescription = sending },
                                    strokeWidth = 2.dp,
                                )
                            }
                            Button(onClick = onSubmit, enabled = state.canSubmit, modifier = Modifier.heightIn(min = 48.dp)) {
                                Icon(Icons.Outlined.Download, contentDescription = null, modifier = Modifier.size(18.dp))
                                Text(stringResource(R.string.add_submit), Modifier.padding(start = 8.dp))
                            }
                        }
                    }
                }
            }
        },
    ) { padding ->
        ScreenFrame(Modifier.padding(padding).consumeWindowInsets(padding)) {
            if (!state.settingsLoaded) {
                LoadingBlock(Modifier.align(Alignment.Center).padding(24.dp))
                return@ScreenFrame
            }
            Column(
                modifier = Modifier
                    .fillMaxSize()
                    .verticalScroll(rememberScrollState())
                    .padding(horizontal = screenGutter(), vertical = 16.dp),
                verticalArrangement = Arrangement.spacedBy(24.dp),
            ) {
                if (!state.serverConfigured) {
                    EmptyState(
                        icon = Icons.Outlined.CloudOff,
                        title = stringResource(R.string.incoming_no_server_title),
                        body = stringResource(R.string.incoming_no_server_body),
                        modifier = Modifier.fillMaxWidth().padding(top = 64.dp),
                        actions = {
                            Button(onClick = onOpenSettings, modifier = Modifier.heightIn(min = 48.dp)) {
                                Text(stringResource(R.string.incoming_open_settings))
                            }
                        },
                    )
                    return@Column
                }
                OutlinedTextField(
                    value = state.url,
                    onValueChange = onUrl,
                    modifier = Modifier.fillMaxWidth(),
                    label = { Text(stringResource(R.string.add_url)) },
                    leadingIcon = { Icon(Icons.Outlined.Link, contentDescription = null) },
                    trailingIcon = {
                        if (state.url.isEmpty()) {
                            IconButton(onClick = {
                                scope.launch { clipboard.getClipEntry()?.clipData?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.text?.toString()?.trim()?.let(onUrl) }
                            }) { Icon(Icons.Outlined.ContentPaste, contentDescription = stringResource(R.string.add_paste)) }
                        } else {
                            IconButton(onClick = { onUrl("") }) { Icon(Icons.Outlined.Close, contentDescription = stringResource(R.string.add_clear)) }
                        }
                    },
                    singleLine = true,
                    isError = state.urlInvalid,
                    supportingText = if (state.urlInvalid) {
                        { Text(stringResource(R.string.add_url_invalid)) }
                    } else null,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                )
                FormSection(stringResource(R.string.field_category), Icons.Outlined.Folder) {
                    when {
                        state.categoriesLoading && state.categories.isEmpty() -> {
                            Text(stringResource(R.string.add_categories_loading))
                            CircularProgressIndicator(Modifier.size(24.dp))
                        }
                        state.categoryError != null && state.categories.isEmpty() -> {
                            Text(
                                stringResource(R.string.add_categories_error, state.categoryError),
                                color = MaterialTheme.colorScheme.error,
                                modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                            )
                            OutlinedButton(onClick = onRefreshCategories, modifier = Modifier.heightIn(min = 48.dp)) {
                                Text(stringResource(R.string.history_error_retry))
                            }
                            if (state.categoryError == ApiError.Unauthorized.userMessage) {
                                TextButton(onClick = onOpenSettings, modifier = Modifier.heightIn(min = 48.dp)) {
                                    Text(stringResource(R.string.queue_snackbar_settings))
                                }
                            }
                        }
                        state.categories.isEmpty() -> {
                            Text(stringResource(if (state.canAddCategory) R.string.add_categories_empty_add else R.string.add_categories_empty))
                            if (state.canAddCategory) {
                                Button(onClick = onAddCategory, modifier = Modifier.heightIn(min = 48.dp)) {
                                    Icon(Icons.Outlined.Add, contentDescription = null, modifier = Modifier.size(18.dp))
                                    Text(stringResource(R.string.server_settings_add_category), Modifier.padding(start = 8.dp))
                                }
                            } else {
                                OutlinedButton(onClick = onOpenServerSettings, modifier = Modifier.heightIn(min = 48.dp)) {
                                    Text(stringResource(R.string.add_manage_categories))
                                }
                            }
                            TextButton(onClick = onRefreshCategories, modifier = Modifier.heightIn(min = 48.dp)) {
                                Text(stringResource(R.string.history_error_retry))
                            }
                        }
                        else -> CategoryChips(
                            state.categories, state.categoryId, onSelectCategory,
                            trailing = if (state.canAddCategory) {
                                {
                                    AssistChip(
                                        onClick = onAddCategory,
                                        label = { Text(stringResource(R.string.server_settings_add_category)) },
                                        leadingIcon = { Icon(Icons.Outlined.Add, contentDescription = null, modifier = Modifier.size(AssistChipDefaults.IconSize)) },
                                        modifier = Modifier.heightIn(min = 48.dp),
                                    )
                                }
                            } else null,
                        )
                    }
                }
                state.layout?.let { layout ->
                    FormSection(stringResource(R.string.add_details), Icons.Outlined.Edit) { ItemFormFields(layout, state.form, actions) }
                }
                val hasAdvanced = state.headers.isNotEmpty() || state.subtitles.isNotEmpty()
                var advancedOpen by rememberSaveable { mutableStateOf(hasAdvanced) }
                AdvancedSection(
                    expanded = advancedOpen,
                    onToggle = { advancedOpen = !advancedOpen },
                    headerCount = state.headers.size,
                    subtitleCount = state.subtitles.size,
                ) {
                FormSection(stringResource(R.string.add_headers), Icons.Outlined.Code) {
                    state.headers.forEach { row ->
                        ResponsiveFormRow(
                            first = { fieldModifier ->
                                OutlinedTextField(
                                    value = row.name,
                                    onValueChange = { onHeaderName(row.id, it) },
                                    modifier = fieldModifier,
                                    label = { Text(stringResource(R.string.add_header_name)) },
                                    singleLine = true,
                                )
                            },
                            second = { fieldModifier ->
                                OutlinedTextField(
                                    value = row.value,
                                    onValueChange = { onHeaderValue(row.id, it) },
                                    modifier = fieldModifier,
                                    label = { Text(stringResource(R.string.add_header_value)) },
                                    singleLine = true,
                                )
                            },
                            trailing = {
                                TextButton(onClick = { onRemoveHeader(row.id) }, modifier = Modifier.heightIn(min = 48.dp)) {
                                    Icon(Icons.Outlined.Close, contentDescription = null, modifier = Modifier.size(18.dp))
                                    Text(stringResource(R.string.add_header_remove), Modifier.padding(start = 8.dp))
                                }
                            },
                        )
                    }
                    FilledTonalButton(onClick = onAddHeader, modifier = Modifier.heightIn(min = 48.dp)) {
                        Icon(Icons.Outlined.Add, contentDescription = null, modifier = Modifier.size(18.dp))
                        Text(stringResource(R.string.add_header_add), modifier = Modifier.padding(start = 8.dp))
                    }
                }
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                FormSection(stringResource(R.string.add_subtitles), Icons.Outlined.Subtitles) {
                    state.subtitles.forEach { row ->
                        ResponsiveFormRow(
                            first = { fieldModifier ->
                                OutlinedTextField(
                                    value = row.url,
                                    onValueChange = { onSubtitleUrl(row.id, it) },
                                    modifier = fieldModifier,
                                    label = { Text(stringResource(R.string.add_subtitle_url)) },
                                    singleLine = true,
                                    isError = row.url.isNotBlank() && !isHttpUrl(row.url),
                                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                                )
                            },
                            second = { fieldModifier ->
                                OutlinedTextField(
                                    value = row.lang,
                                    onValueChange = { onSubtitleLanguage(row.id, it) },
                                    modifier = fieldModifier,
                                    label = { Text(stringResource(R.string.add_subtitle_lang)) },
                                    singleLine = true,
                                )
                            },
                            trailing = {
                                TextButton(onClick = { onRemoveSubtitle(row.id) }, modifier = Modifier.heightIn(min = 48.dp)) {
                                    Icon(Icons.Outlined.Close, contentDescription = null, modifier = Modifier.size(18.dp))
                                    Text(stringResource(R.string.add_subtitle_remove), Modifier.padding(start = 8.dp))
                                }
                            },
                        )
                    }
                    if (state.subtitleInvalid) {
                        Text(stringResource(R.string.add_subtitle_url_invalid), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                    }
                    FilledTonalButton(onClick = onAddSubtitle, modifier = Modifier.heightIn(min = 48.dp)) {
                        Icon(Icons.Outlined.Add, contentDescription = null, modifier = Modifier.size(18.dp))
                        Text(stringResource(R.string.add_subtitle_add), modifier = Modifier.padding(start = 8.dp))
                    }
                }
                }
            }
        }
    }
}

/** Adds a category to the server without leaving the form; the server creates the folder. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun NewCategoryDialog(
    draft: NewCategoryDraft,
    onEdit: ((NewCategoryDraft) -> NewCategoryDraft) -> Unit,
    onDismiss: () -> Unit,
    onSave: () -> Unit,
) {
    val enabled = !draft.saving
    AlertDialog(
        onDismissRequest = onDismiss,
        properties = DialogProperties(dismissOnBackPress = enabled, dismissOnClickOutside = enabled),
        title = { Text(stringResource(R.string.server_settings_new_category)) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                OutlinedTextField(
                    value = draft.label,
                    onValueChange = { v -> onEdit { it.copy(label = v) } },
                    modifier = Modifier.fillMaxWidth(),
                    label = { Text(stringResource(R.string.server_settings_label)) },
                    enabled = enabled,
                    singleLine = true,
                    isError = draft.showErrors && draft.labelInvalid,
                    supportingText = if (draft.showErrors && draft.labelInvalid) {
                        { Text(stringResource(R.string.server_settings_required)) }
                    } else null,
                )
                OutlinedTextField(
                    value = draft.path,
                    onValueChange = { v -> onEdit { it.copy(path = v) } },
                    modifier = Modifier.fillMaxWidth(),
                    label = { Text(stringResource(R.string.server_settings_destination)) },
                    placeholder = { Text("/media/…") },
                    enabled = enabled,
                    singleLine = true,
                    isError = draft.showErrors && draft.pathInvalid,
                    supportingText = {
                        Text(stringResource(if (draft.showErrors && draft.pathInvalid) R.string.server_settings_path_invalid else R.string.add_category_path_hint))
                    },
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                )
                Text(stringResource(R.string.server_settings_layout), style = MaterialTheme.typography.labelLarge)
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    listOf(
                        CategoryOption.LAYOUT_MOVIE to R.string.server_settings_movie,
                        CategoryOption.LAYOUT_TV to R.string.server_settings_tv,
                        CategoryOption.LAYOUT_FLAT to R.string.server_settings_flat,
                    ).forEach { (layout, title) ->
                        FilterChip(
                            selected = draft.layout == layout,
                            onClick = { onEdit { it.copy(layout = layout) } },
                            label = { Text(stringResource(title)) },
                            leadingIcon = { Icon(layoutIcon(layout), contentDescription = null, modifier = Modifier.size(FilterChipDefaults.IconSize)) },
                            enabled = enabled,
                            modifier = Modifier.heightIn(min = 48.dp),
                        )
                    }
                }
                draft.error?.let { error ->
                    Text(
                        error,
                        color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.bodyMedium,
                        modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                    )
                }
            }
        },
        confirmButton = {
            TextButton(onClick = onSave, enabled = enabled, modifier = Modifier.heightIn(min = 48.dp)) {
                Text(stringResource(if (draft.saving) R.string.server_settings_saving else R.string.add_category_create))
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss, enabled = enabled, modifier = Modifier.heightIn(min = 48.dp)) {
                Text(stringResource(R.string.incoming_cancel))
            }
        },
    )
}

/** Headers and subtitles are rarely needed: one quiet row until the user asks for them. */
@Composable
private fun AdvancedSection(
    expanded: Boolean,
    onToggle: () -> Unit,
    headerCount: Int,
    subtitleCount: Int,
    content: @Composable ColumnScope.() -> Unit,
) {
    val colors = MaterialTheme.colorScheme
    val chevron by animateFloatAsState(if (expanded) 180f else 0f, label = "advanced")
    val summary = listOfNotNull(
        headerCount.takeIf { it > 0 }?.let { pluralStringResource(R.plurals.add_headers_count, it, it) },
        subtitleCount.takeIf { it > 0 }?.let { pluralStringResource(R.plurals.add_subtitles_count, it, it) },
    ).joinToString(" · ").ifEmpty { stringResource(R.string.add_advanced_hint) }
    Surface(
        shape = MaterialTheme.shapes.large,
        color = Color.Transparent,
        border = BorderStroke(1.dp, colors.outlineVariant),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Column {
            Row(
                Modifier
                    .fillMaxWidth()
                    .clickable(onClickLabel = stringResource(if (expanded) R.string.add_advanced_collapse else R.string.add_advanced_expand), onClick = onToggle)
                    .heightIn(min = 64.dp)
                    .padding(horizontal = 16.dp, vertical = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(16.dp),
            ) {
                Icon(Icons.Outlined.Tune, contentDescription = null, tint = colors.primary, modifier = Modifier.size(20.dp))
                Column(Modifier.weight(1f)) {
                    Text(stringResource(R.string.add_advanced), style = MaterialTheme.typography.titleSmall)
                    Text(summary, style = MaterialTheme.typography.bodySmall, color = colors.onSurfaceVariant)
                }
                Icon(Icons.Outlined.ExpandMore, contentDescription = null, tint = colors.onSurfaceVariant, modifier = Modifier.rotate(chevron))
            }
            AnimatedVisibility(visible = expanded) {
                Column(Modifier.padding(start = 16.dp, end = 16.dp, bottom = 16.dp), verticalArrangement = Arrangement.spacedBy(16.dp), content = content)
            }
        }
    }
}
