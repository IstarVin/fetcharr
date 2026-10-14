package dev.istarvin.fetcharr.ui.add

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
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
import dev.istarvin.fetcharr.R
import dev.istarvin.fetcharr.data.network.ApiError
import dev.istarvin.fetcharr.ui.incoming.CategoryChips
import dev.istarvin.fetcharr.ui.incoming.ItemFormActions
import dev.istarvin.fetcharr.ui.incoming.ItemFormFields
import dev.istarvin.fetcharr.ui.incoming.isHttpUrl

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
    ) { padding ->
        if (!state.settingsLoaded) return@Scaffold
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .imePadding()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 16.dp, vertical = 8.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            if (!state.serverConfigured) {
                Text(stringResource(R.string.incoming_no_server_title), style = MaterialTheme.typography.titleMedium)
                Text(
                    stringResource(R.string.incoming_no_server_body),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Button(onClick = onOpenSettings) { Text(stringResource(R.string.incoming_open_settings)) }
                return@Column
            }
            OutlinedTextField(
                value = state.url,
                onValueChange = viewModel::onUrl,
                modifier = Modifier.fillMaxWidth(),
                label = { Text(stringResource(R.string.add_url)) },
                singleLine = true,
                isError = state.urlInvalid,
                supportingText = if (state.urlInvalid) {
                    { Text(stringResource(R.string.add_url_invalid)) }
                } else {
                    null
                },
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
            )
            Text(stringResource(R.string.field_category), style = MaterialTheme.typography.titleSmall)
            CategoryChips(state.categories, state.categoryId, viewModel::selectCategory)
            state.layout?.let { ItemFormFields(it, state.form, actions) }

            Text(stringResource(R.string.add_headers), style = MaterialTheme.typography.titleSmall)
            state.headers.forEach { row ->
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                    OutlinedTextField(
                        value = row.name,
                        onValueChange = { viewModel.updateHeader(row.id, name = it) },
                        modifier = Modifier.weight(1f),
                        label = { Text(stringResource(R.string.add_header_name)) },
                        singleLine = true,
                    )
                    OutlinedTextField(
                        value = row.value,
                        onValueChange = { viewModel.updateHeader(row.id, value = it) },
                        modifier = Modifier.weight(1f),
                        label = { Text(stringResource(R.string.add_header_value)) },
                        singleLine = true,
                    )
                    IconButton(onClick = { viewModel.removeHeader(row.id) }) {
                        Icon(Icons.Outlined.Close, contentDescription = stringResource(R.string.add_header_remove))
                    }
                }
            }
            OutlinedButton(onClick = viewModel::addHeader) {
                Icon(Icons.Outlined.Add, contentDescription = null, modifier = Modifier.size(18.dp))
                Text(stringResource(R.string.add_header_add), modifier = Modifier.padding(start = 8.dp))
            }

            Text(stringResource(R.string.add_subtitles), style = MaterialTheme.typography.titleSmall)
            state.subtitles.forEach { row ->
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                    OutlinedTextField(
                        value = row.url,
                        onValueChange = { viewModel.updateSubtitle(row.id, url = it) },
                        modifier = Modifier.weight(2f),
                        label = { Text(stringResource(R.string.add_subtitle_url)) },
                        singleLine = true,
                        isError = row.url.isNotBlank() && !isHttpUrl(row.url),
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                    )
                    OutlinedTextField(
                        value = row.lang,
                        onValueChange = { viewModel.updateSubtitle(row.id, lang = it) },
                        modifier = Modifier.weight(1f),
                        label = { Text(stringResource(R.string.add_subtitle_lang)) },
                        singleLine = true,
                    )
                    IconButton(onClick = { viewModel.removeSubtitle(row.id) }) {
                        Icon(Icons.Outlined.Close, contentDescription = stringResource(R.string.add_subtitle_remove))
                    }
                }
            }
            if (state.subtitleInvalid) {
                Text(
                    stringResource(R.string.add_subtitle_url_invalid),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            OutlinedButton(onClick = viewModel::addSubtitle) {
                Icon(Icons.Outlined.Add, contentDescription = null, modifier = Modifier.size(18.dp))
                Text(stringResource(R.string.add_subtitle_add), modifier = Modifier.padding(start = 8.dp))
            }

            state.error?.let {
                Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium)
                if (it == ApiError.Unauthorized.userMessage) {
                    TextButton(onClick = onOpenSettings) { Text(stringResource(R.string.queue_snackbar_settings)) }
                }
            }
            Row(
                Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(8.dp, Alignment.End),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                if (state.submitting) {
                    val sending = stringResource(R.string.a11y_sending)
                    CircularProgressIndicator(Modifier.size(24.dp).semantics { contentDescription = sending }, strokeWidth = 2.dp)
                }
                Button(onClick = viewModel::submit, enabled = state.canSubmit) {
                    Text(stringResource(R.string.add_submit))
                }
            }
        }
    }
}
