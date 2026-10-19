package com.istarvin.fetcharr.ui.history

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.istarvin.fetcharr.R
import com.istarvin.fetcharr.ui.add.HeaderRow
import com.istarvin.fetcharr.ui.add.SubtitleRow
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/** Sensitive inputs live only in memory, never SavedStateHandle. */
data class SourceDraft(
    val jobId: String, val name: String, val url: String,
    val headersMode: String = "clear",
    val headers: List<HeaderRow> = emptyList(), val subtitles: List<SubtitleRow> = emptyList(),
    val needsOverwrite: Boolean = false, val confirmOverwrite: Boolean = false,
    val submitting: Boolean = false, val error: String? = null,
) {
    val urlValid: Boolean get() = validSourceUrl(url)
    val headersValid: Boolean get() = headersMode != "replace" ||
        (headers.all { row -> row.name.trim().isNotEmpty() && row.name.trim().none { it == ':' || it.isWhitespace() || it.isISOControl() } && row.value.none(Char::isISOControl) } &&
            headers.map { it.name.trim().lowercase(java.util.Locale.ROOT) }.distinct().size == headers.size)
    val subtitlesValid: Boolean get() = subtitles.all { it.url.isBlank() || validSourceUrl(it.url) }
    val canSubmit: Boolean get() = !submitting && urlValid && headersValid && subtitlesValid
}
private fun validSourceUrl(value: String): Boolean = value.trim().toHttpUrlOrNull() != null && value.trim().none(Char::isWhitespace)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun SourceReplacementEditor(
    draft: SourceDraft, edit: ((SourceDraft) -> SourceDraft) -> Unit,
    addHeader: () -> Unit, addSubtitle: () -> Unit, submit: () -> Unit,
    confirmOverwrite: () -> Unit, dismiss: () -> Unit,
) {
    var confirmLeave by remember(draft.jobId) { mutableStateOf(false) }
    val requestLeave = { if (draft.submitting) confirmLeave = true else dismiss() }
    Dialog(onDismissRequest = requestLeave, properties = DialogProperties(usePlatformDefaultWidth = false, decorFitsSystemWindows = false)) {
        BackHandler(onBack = requestLeave)
        Surface(Modifier.fillMaxSize()) {
            Scaffold(modifier = Modifier.safeDrawingPadding().imePadding(), topBar = {
                TopAppBar(title = { Text(stringResource(R.string.source_replace_title)) }, navigationIcon = {
                    IconButton(onClick = requestLeave) { Icon(Icons.AutoMirrored.Outlined.ArrowBack, stringResource(R.string.source_back)) }
                })
            }, bottomBar = {
                Surface(tonalElevation = 3.dp) {
                    Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
                        Column(Modifier.widthIn(max = 640.dp).fillMaxWidth().padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            Text(stringResource(R.string.source_fresh_download), style = MaterialTheme.typography.bodySmall)
                            draft.error?.let { Text(it, color = MaterialTheme.colorScheme.error, modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite }) }
                            Button(onClick = submit, enabled = draft.canSubmit, modifier = Modifier.fillMaxWidth().heightIn(min = 48.dp)) {
                                Text(stringResource(if (draft.submitting) R.string.source_submitting else R.string.source_replace_action))
                            }
                        }
                    }
                }
            }) { padding ->
                Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.TopCenter) {
                    LazyColumn(Modifier.widthIn(max = 640.dp).fillMaxSize(), contentPadding = PaddingValues(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                        item { Text(draft.name, style = MaterialTheme.typography.titleMedium) }
                        item {
                            OutlinedTextField(draft.url, { v -> edit { it.copy(url = v) } }, label = { Text(stringResource(R.string.source_url)) },
                                enabled = !draft.submitting, isError = !draft.urlValid, modifier = Modifier.fillMaxWidth(),
                                supportingText = { if (!draft.urlValid) Text(stringResource(R.string.source_url_error)) })
                        }
                        item { Text(stringResource(R.string.source_headers), style = MaterialTheme.typography.titleSmall) }
                        item {
                            Column {
                                listOf("clear" to R.string.source_headers_clear, "keep" to R.string.source_headers_keep, "replace" to R.string.source_headers_replace).forEach { (mode, label) ->
                                    FilterChip(selected = draft.headersMode == mode, onClick = { edit { it.copy(headersMode = mode) } }, enabled = !draft.submitting,
                                        label = { Text(stringResource(label)) }, modifier = Modifier.heightIn(min = 48.dp))
                                }
                            }
                        }
                        if (draft.headersMode == "replace") {
                            items(draft.headers, key = { "header:${it.id}" }) { row ->
                                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                                    SourceField(row.name, R.string.source_header_name, !draft.submitting) { v -> edit { s -> s.copy(headers = s.headers.map { if (it.id == row.id) it.copy(name = v) else it }) } }
                                    SourceField(row.value, R.string.source_header_value, !draft.submitting) { v -> edit { s -> s.copy(headers = s.headers.map { if (it.id == row.id) it.copy(value = v) else it }) } }
                                    TextButton(onClick = { edit { s -> s.copy(headers = s.headers.filterNot { it.id == row.id }) } }, enabled = !draft.submitting) { Text(stringResource(R.string.source_remove_header)) }
                                }
                            }
                            item {
                                Column {
                                    if (!draft.headersValid) Text(stringResource(R.string.source_headers_error), color = MaterialTheme.colorScheme.error)
                                    OutlinedButton(onClick = addHeader, enabled = !draft.submitting) { Text(stringResource(R.string.source_add_header)) }
                                }
                            }
                        }
                        item { Text(stringResource(R.string.source_subtitles), style = MaterialTheme.typography.titleSmall) }
                        items(draft.subtitles, key = { "subtitle:${it.id}" }) { row ->
                            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                                OutlinedTextField(row.url, { v -> edit { s -> s.copy(subtitles = s.subtitles.map { if (it.id == row.id) it.copy(url = v) else it }) } }, label = { Text(stringResource(R.string.source_subtitle_url)) },
                                    enabled = !draft.submitting, isError = row.url.isNotBlank() && !validSourceUrl(row.url), modifier = Modifier.fillMaxWidth())
                                SourceField(row.lang, R.string.source_subtitle_language, !draft.submitting) { v -> edit { s -> s.copy(subtitles = s.subtitles.map { if (it.id == row.id) it.copy(lang = v) else it }) } }
                                TextButton(onClick = { edit { s -> s.copy(subtitles = s.subtitles.filterNot { it.id == row.id }) } }, enabled = !draft.submitting) { Text(stringResource(R.string.source_remove_subtitle)) }
                            }
                        }
                        item { OutlinedButton(onClick = addSubtitle, enabled = !draft.submitting) { Text(stringResource(R.string.source_add_subtitle)) } }
                    }
                }
            }
        }
        if (confirmLeave) AlertDialog(onDismissRequest = { confirmLeave = false }, title = { Text(stringResource(R.string.source_leave_title)) }, text = { Text(stringResource(R.string.source_leave_body)) },
            confirmButton = { TextButton(onClick = dismiss) { Text(stringResource(R.string.source_leave)) } }, dismissButton = { TextButton(onClick = { confirmLeave = false }) { Text(stringResource(R.string.source_stay)) } })
        if (draft.confirmOverwrite) AlertDialog(onDismissRequest = { edit { it.copy(confirmOverwrite = false) } }, title = { Text(stringResource(R.string.history_replace_title)) }, text = { Text(stringResource(R.string.history_replace_body)) },
            confirmButton = { TextButton(onClick = confirmOverwrite) { Text(stringResource(R.string.history_replace_confirm)) } }, dismissButton = { TextButton(onClick = { edit { it.copy(confirmOverwrite = false) } }) { Text(stringResource(R.string.source_stay)) } })
    }
}

@Composable
private fun SourceField(value: String, label: Int, enabled: Boolean, change: (String) -> Unit) {
    OutlinedTextField(value, change, label = { Text(stringResource(label)) }, enabled = enabled, singleLine = true, modifier = Modifier.fillMaxWidth())
}
