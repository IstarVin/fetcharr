package com.istarvin.fetcharr.ui.add

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
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
import androidx.compose.material3.Button
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
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.istarvin.fetcharr.ui.common.screenGutter
import com.istarvin.fetcharr.R
import com.istarvin.fetcharr.data.network.ApiError
import com.istarvin.fetcharr.ui.common.ScreenFrame
import com.istarvin.fetcharr.ui.incoming.FormFooter
import com.istarvin.fetcharr.ui.incoming.FormSection
import com.istarvin.fetcharr.ui.incoming.ResponsiveFormRow
import com.istarvin.fetcharr.ui.incoming.CategoryChips
import com.istarvin.fetcharr.ui.incoming.ItemFormActions
import com.istarvin.fetcharr.ui.incoming.ItemFormFields
import com.istarvin.fetcharr.ui.incoming.isHttpUrl

/** Manual add (spec 5.2). The URL is pre-filled from the share sheet via the route's `url` argument. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AddScreen(
    onBack: () -> Unit,
    onOpenSettings: () -> Unit,
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
) {
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
                    if (state.submitting) {
                        val sending = stringResource(R.string.a11y_sending)
                        CircularProgressIndicator(
                            Modifier.size(24.dp).semantics { contentDescription = sending },
                            strokeWidth = 2.dp,
                        )
                    }
                    Button(onClick = onSubmit, enabled = state.canSubmit, modifier = Modifier.heightIn(min = 48.dp)) {
                        Text(stringResource(R.string.add_submit))
                    }
                }
            }
        },
    ) { padding ->
        ScreenFrame(Modifier.padding(padding).consumeWindowInsets(padding)) {
            if (!state.settingsLoaded) {
                Column(
                    Modifier.align(Alignment.Center).padding(24.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.spacedBy(16.dp),
                ) {
                    CircularProgressIndicator()
                    Text(stringResource(R.string.a11y_loading), style = MaterialTheme.typography.bodyMedium)
                }
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
                    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                        Text(stringResource(R.string.incoming_no_server_title), style = MaterialTheme.typography.titleMedium)
                        Text(
                            stringResource(R.string.incoming_no_server_body),
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Button(onClick = onOpenSettings, modifier = Modifier.heightIn(min = 48.dp)) {
                            Text(stringResource(R.string.incoming_open_settings))
                        }
                    }
                    return@Column
                }
                OutlinedTextField(
                    value = state.url,
                    onValueChange = onUrl,
                    modifier = Modifier.fillMaxWidth(),
                    label = { Text(stringResource(R.string.add_url)) },
                    singleLine = true,
                    isError = state.urlInvalid,
                    supportingText = if (state.urlInvalid) {
                        { Text(stringResource(R.string.add_url_invalid)) }
                    } else null,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                )
                FormSection(stringResource(R.string.field_category)) {
                    CategoryChips(state.categories, state.categoryId, onSelectCategory)
                }
                state.layout?.let { ItemFormFields(it, state.form, actions) }
                FormSection(stringResource(R.string.add_headers)) {
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
                    OutlinedButton(onClick = onAddHeader, modifier = Modifier.heightIn(min = 48.dp)) {
                        Icon(Icons.Outlined.Add, contentDescription = null, modifier = Modifier.size(18.dp))
                        Text(stringResource(R.string.add_header_add), modifier = Modifier.padding(start = 8.dp))
                    }
                }
                FormSection(stringResource(R.string.add_subtitles)) {
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
                    OutlinedButton(onClick = onAddSubtitle, modifier = Modifier.heightIn(min = 48.dp)) {
                        Icon(Icons.Outlined.Add, contentDescription = null, modifier = Modifier.size(18.dp))
                        Text(stringResource(R.string.add_subtitle_add), modifier = Modifier.padding(start = 8.dp))
                    }
                }
                state.error?.let {
                    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium)
                        if (it == ApiError.Unauthorized.userMessage) {
                            TextButton(onClick = onOpenSettings, modifier = Modifier.heightIn(min = 48.dp)) {
                                Text(stringResource(R.string.queue_snackbar_settings))
                            }
                        }
                    }
                }
            }
        }
    }
}
