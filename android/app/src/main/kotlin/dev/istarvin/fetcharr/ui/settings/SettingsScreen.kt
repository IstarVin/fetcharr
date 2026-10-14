package dev.istarvin.fetcharr.ui.settings

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
import androidx.compose.material.icons.outlined.Visibility
import androidx.compose.material.icons.outlined.VisibilityOff
import androidx.compose.material3.Button
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
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.istarvin.fetcharr.BuildConfig
import dev.istarvin.fetcharr.R
import dev.istarvin.fetcharr.data.network.TestConnectionResult
import dev.istarvin.fetcharr.data.network.dto.CategoryDto
import dev.istarvin.fetcharr.ui.LocalSnackbarHostState
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(viewModel: SettingsViewModel = hiltViewModel()) {
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

            GroupTitle(R.string.settings_group_about)
            Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                Text(stringResource(R.string.settings_app_version), style = MaterialTheme.typography.bodyLarge)
                Text(
                    BuildConfig.VERSION_NAME,
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
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
