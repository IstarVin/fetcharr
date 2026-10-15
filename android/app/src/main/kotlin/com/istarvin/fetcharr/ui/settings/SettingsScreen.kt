package com.istarvin.fetcharr.ui.settings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Visibility
import androidx.compose.material.icons.outlined.VisibilityOff
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExposedDropdownMenuAnchorType
import androidx.compose.material3.ExposedDropdownMenuBox
import androidx.compose.material3.ExposedDropdownMenuDefaults
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
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.istarvin.fetcharr.BuildConfig
import com.istarvin.fetcharr.R
import com.istarvin.fetcharr.data.network.TestConnectionResult
import com.istarvin.fetcharr.data.network.dto.CategoryDto
import com.istarvin.fetcharr.ui.LocalSnackbarHostState
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(viewModel: SettingsViewModel = hiltViewModel(), onServerSettings: () -> Unit = {}) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    val snackbar = LocalSnackbarHostState.current
    val scope = rememberCoroutineScope()
    val savedMessage = stringResource(R.string.settings_saved)

    Scaffold(topBar = { TopAppBar(title = { Text(stringResource(R.string.nav_settings)) }) }) { padding ->
        if (!state.loaded) return@Scaffold
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 16.dp, vertical = 8.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            GroupTitle(R.string.settings_group_server)
            OutlinedTextField(
                value = state.url,
                onValueChange = viewModel::onUrlChange,
                modifier = Modifier.fillMaxWidth(),
                label = { Text(stringResource(R.string.settings_server_url)) },
                placeholder = { Text(stringResource(R.string.settings_server_url_hint)) },
                singleLine = true,
                isError = state.urlInvalid,
                supportingText = if (state.urlInvalid) {
                    { Text(stringResource(R.string.settings_server_url_invalid)) }
                } else {
                    null
                },
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
            )
            OutlinedTextField(
                value = state.token,
                onValueChange = viewModel::onTokenChange,
                modifier = Modifier.fillMaxWidth(),
                label = { Text(stringResource(R.string.settings_token)) },
                singleLine = true,
                visualTransformation = if (state.tokenVisible) VisualTransformation.None else PasswordVisualTransformation(),
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                trailingIcon = {
                    IconButton(onClick = viewModel::toggleTokenVisible) {
                        Icon(
                            imageVector = if (state.tokenVisible) Icons.Outlined.VisibilityOff else Icons.Outlined.Visibility,
                            contentDescription = stringResource(
                                if (state.tokenVisible) R.string.settings_token_hide else R.string.settings_token_show,
                            ),
                        )
                    }
                },
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = {
                    if (viewModel.save()) scope.launch { snackbar.showSnackbar(savedMessage) }
                }) { Text(stringResource(R.string.settings_save)) }
                OutlinedButton(
                    onClick = viewModel::testConnection,
                    enabled = state.testStatus != TestStatus.Running,
                ) {
                    Text(
                        stringResource(
                            if (state.testStatus == TestStatus.Running) R.string.settings_testing else R.string.settings_test,
                        ),
                    )
                }
            }
            TestResultText(state.testStatus)

            OutlinedButton(onClick = onServerSettings, enabled = state.hasSavedServer) {
                Text(stringResource(R.string.server_settings_title))
            }

            GroupTitle(R.string.settings_group_defaults)
            CategoryDropdown(
                label = R.string.settings_default_movie,
                categories = state.movieCategories,
                selectedId = state.defaultMovieCategoryId,
                onSelect = viewModel::onDefaultMovieCategory,
            )
            CategoryDropdown(
                label = R.string.settings_default_tv,
                categories = state.tvCategories,
                selectedId = state.defaultTvCategoryId,
                onSelect = viewModel::onDefaultTvCategory,
            )
            if (state.movieCategories.isEmpty() && state.tvCategories.isEmpty()) {
                Text(
                    stringResource(R.string.settings_defaults_hint),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            if (state.serverVersion != null) YtdlpSection(state, onUpdate = viewModel::updateYtdlp)

            GroupTitle(R.string.settings_group_about)
            AboutRow(stringResource(R.string.settings_app_version), BuildConfig.VERSION_NAME)
            AboutRow(
                stringResource(R.string.settings_server_version),
                state.serverVersion ?: stringResource(R.string.settings_no_value),
            )
            val uriHandler = LocalUriHandler.current
            val sourceUrl = stringResource(R.string.settings_source_code_url)
            TextButton(
                onClick = { uriHandler.openUri(sourceUrl) },
                contentPadding = PaddingValues(0.dp),
            ) {
                Text(stringResource(R.string.settings_source_code), modifier = Modifier.fillMaxWidth())
            }
        }
    }
}

@Composable
private fun AboutRow(label: String, value: String) {
    Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
        Text(label, style = MaterialTheme.typography.bodyLarge)
        Text(
            value,
            style = MaterialTheme.typography.bodyLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/** yt-dlp version + "Update now". The button is hidden when the server doesn't manage yt-dlp. */
@Composable
private fun YtdlpSection(state: SettingsUiState, onUpdate: () -> Unit) {
    val update = state.ytdlpUpdate
    val version = state.ytdlpVersion
        ?: (update as? YtdlpUpdateStatus.Updated)?.version
    GroupTitle(R.string.settings_group_ytdlp)
    AboutRow(
        stringResource(R.string.settings_ytdlp_version),
        version ?: stringResource(R.string.settings_ytdlp_unknown),
    )
    val running = update == YtdlpUpdateStatus.Running
    if (state.ytdlpManaged) {
        val updatingDesc = stringResource(R.string.settings_ytdlp_updating_desc)
        OutlinedButton(onClick = onUpdate, enabled = !running) {
            if (running) {
                CircularProgressIndicator(
                    modifier = Modifier
                        .size(16.dp)
                        .semantics { contentDescription = updatingDesc },
                    strokeWidth = 2.dp,
                )
                Spacer(Modifier.width(8.dp))
            }
            Text(stringResource(if (running) R.string.settings_ytdlp_updating else R.string.settings_ytdlp_update))
        }
    }
    when (update) {
        is YtdlpUpdateStatus.Updated -> Text(
            update.version?.let { stringResource(R.string.settings_ytdlp_updated, it) }
                ?: stringResource(R.string.settings_ytdlp_updated_unknown),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.primary,
        )
        is YtdlpUpdateStatus.Failed -> Text(
            update.error.userMessage,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.error,
        )
        else -> Unit
    }
}

@Composable
private fun GroupTitle(res: Int) {
    Text(
        stringResource(res),
        style = MaterialTheme.typography.titleSmall,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(top = 8.dp),
    )
}

@Composable
private fun TestResultText(status: TestStatus) {
    if (status !is TestStatus.Done) return
    val result = status.result
    val text = when (result) {
        is TestConnectionResult.Connected -> stringResource(R.string.settings_test_connected, result.version, result.latencyMs)
        TestConnectionResult.Unreachable -> stringResource(R.string.settings_test_unreachable)
        TestConnectionResult.WrongToken -> stringResource(R.string.settings_test_wrong_token)
        TestConnectionResult.NotFetcharr -> stringResource(R.string.settings_test_not_fetcharr)
    }
    Text(
        text,
        style = MaterialTheme.typography.bodyMedium,
        color = if (result is TestConnectionResult.Connected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.error,
    )
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun CategoryDropdown(
    label: Int,
    categories: List<CategoryDto>,
    selectedId: String?,
    onSelect: (String?) -> Unit,
) {
    var expanded by remember { mutableStateOf(false) }
    val none = stringResource(R.string.settings_default_none)
    val selectedText = selectedId?.let { id -> categories.firstOrNull { it.id == id }?.label ?: id } ?: none
    ExposedDropdownMenuBox(expanded = expanded, onExpandedChange = { expanded = it }) {
        OutlinedTextField(
            value = selectedText,
            onValueChange = {},
            readOnly = true,
            singleLine = true,
            label = { Text(stringResource(label)) },
            trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded = expanded) },
            modifier = Modifier
                .fillMaxWidth()
                .menuAnchor(ExposedDropdownMenuAnchorType.PrimaryNotEditable),
        )
        ExposedDropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            DropdownMenuItem(
                text = { Text(none) },
                onClick = {
                    onSelect(null)
                    expanded = false
                },
            )
            categories.forEach { category ->
                DropdownMenuItem(
                    text = { Text(category.label) },
                    onClick = {
                        onSelect(category.id)
                        expanded = false
                    },
                )
            }
        }
    }
}
