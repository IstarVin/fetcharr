package dev.istarvin.fetcharr.ui.history

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.os.Build
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyItemScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.selection.toggleable
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.CloudOff
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.ErrorOutline
import androidx.compose.material.icons.outlined.History
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material.icons.outlined.Replay
import androidx.compose.material.icons.outlined.Warning
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarResult
import androidx.compose.material3.Surface
import androidx.compose.material3.SwipeToDismissBox
import androidx.compose.material3.SwipeToDismissBoxValue
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.material3.rememberSwipeToDismissBoxState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.CustomAccessibilityAction
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.customActions
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.istarvin.fetcharr.R
import dev.istarvin.fetcharr.data.network.dto.JobStatus
import dev.istarvin.fetcharr.ui.LocalSnackbarHostState
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch

/** Start loading the next page when the last visible item is this close to the end. */
private const val LOAD_MORE_THRESHOLD = 8

@Composable
fun HistoryScreen(onOpenSettings: () -> Unit, viewModel: HistoryViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val snackbar = LocalSnackbarHostState.current
    val context = LocalContext.current
    val settingsLabel = stringResource(R.string.queue_snackbar_settings)
    val copiedMessage = stringResource(R.string.history_path_copied)

    LaunchedEffect(viewModel) {
        viewModel.events.collect { event ->
            when (event) {
                is HistoryEvent.Snackbar -> {
                    snackbar.currentSnackbarData?.dismiss()
                    launch {
                        val result = snackbar.showSnackbar(
                            message = event.message,
                            actionLabel = if (event.openSettings) settingsLabel else null,
                        )
                        if (result == SnackbarResult.ActionPerformed) onOpenSettings()
                    }
                }
                is HistoryEvent.CopyPath -> {
                    val clipboard = context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
                    clipboard.setPrimaryClip(ClipData.newPlainText("path", event.path))
                    // Android 13+ confirms a copy itself.
                    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) {
                        snackbar.currentSnackbarData?.dismiss()
                        launch { snackbar.showSnackbar(copiedMessage) }
                    }
                }
            }
        }
    }

    HistoryContent(
        state = state,
        onOpenSettings = onOpenSettings,
        onRefresh = viewModel::refresh,
        onLoadMore = viewModel::loadMore,
        onAction = { action, id ->
            when (action) {
                HistoryAction.CopyPath -> viewModel.copyPath(id)
                HistoryAction.Retry -> viewModel.retry(id)
                HistoryAction.Details -> viewModel.showDetails(id)
                HistoryAction.Delete -> viewModel.requestDelete(id)
            }
        },
        onDeleteSwiped = viewModel::requestDelete,
        onClear = viewModel::requestClear,
    )

    when (val dialog = state.dialog) {
        null -> Unit
        is HistoryDialog.Delete -> DeleteDialog(dialog, viewModel::setDeleteFile, viewModel::confirmDelete, viewModel::dismissDialog)
        is HistoryDialog.ConfirmReplace -> ConfirmDialog(
            title = R.string.history_replace_title,
            body = stringResource(R.string.history_replace_body),
            confirm = R.string.history_replace_confirm,
            onConfirm = viewModel::confirmReplace,
            onDismiss = viewModel::dismissDialog,
        )
        is HistoryDialog.Details -> AlertDialog(
            onDismissRequest = viewModel::dismissDialog,
            title = { Text(dialog.name) },
            text = { Text(dialog.message) },
            confirmButton = { TextButton(onClick = viewModel::dismissDialog) { Text(stringResource(R.string.history_dialog_close)) } },
        )
        HistoryDialog.ClearAll -> ConfirmDialog(
            title = R.string.history_clear_title,
            body = stringResource(R.string.history_clear_body),
            confirm = R.string.history_clear_confirm,
            onConfirm = viewModel::confirmClear,
            onDismiss = viewModel::dismissDialog,
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class, ExperimentalFoundationApi::class)
@Composable
internal fun HistoryContent(
    state: HistoryUiState,
    onOpenSettings: () -> Unit,
    onRefresh: () -> Unit,
    onLoadMore: () -> Unit,
    onAction: (HistoryAction, String) -> Unit,
    onDeleteSwiped: (String) -> Unit,
    onClear: () -> Unit,
) {
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.nav_history)) },
                actions = {
                    if (state.hasServer) {
                        var menuOpen by remember { mutableStateOf(false) }
                        Box {
                            IconButton(onClick = { menuOpen = true }) {
                                Icon(Icons.Outlined.MoreVert, contentDescription = stringResource(R.string.history_menu_more))
                            }
                            DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                                DropdownMenuItem(
                                    text = { Text(stringResource(R.string.history_clear)) },
                                    enabled = state.groups.isNotEmpty(),
                                    onClick = { menuOpen = false; onClear() },
                                )
                            }
                        }
                    }
                },
            )
        },
    ) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            when {
                !state.loaded -> Unit
                !state.hasServer -> SetupState(onOpenSettings)
                else -> {
                    val listState = rememberLazyListState()
                    val loadMore by rememberUpdatedState(onLoadMore)
                    LaunchedEffect(listState) {
                        snapshotFlow {
                            val info = listState.layoutInfo
                            (info.visibleItemsInfo.lastOrNull()?.index ?: -1) to info.totalItemsCount
                        }.distinctUntilChanged().collect { (last, total) ->
                            if (last >= 0 && last >= total - LOAD_MORE_THRESHOLD) loadMore()
                        }
                    }
                    PullToRefreshBox(
                        isRefreshing = state.isRefreshing,
                        onRefresh = onRefresh,
                        modifier = Modifier.fillMaxSize(),
                    ) {
                        LazyColumn(
                            state = listState,
                            modifier = Modifier.fillMaxSize(),
                            contentPadding = PaddingValues(bottom = 16.dp),
                        ) {
                            when {
                                !state.historyLoaded && state.error != null ->
                                    item(key = "error") { ErrorState(state.error.userMessage, onRefresh) }
                                !state.historyLoaded -> item(key = "loading") { LoadingState() }
                                state.isEmpty -> item(key = "empty") { EmptyHistory() }
                                else -> {
                                    for (group in state.groups) {
                                        stickyHeader(key = "header:${group.key}") { DayHeader(group.label) }
                                        items(group.items, key = { it.id }) { row ->
                                            HistoryRow(row, onAction = { onAction(it, row.id) }, onDeleteSwiped = { onDeleteSwiped(row.id) })
                                        }
                                    }
                                    if (state.loadingMore) {
                                        item(key = "loading-more") {
                                            val description = stringResource(R.string.history_cd_loading_more)
                                            Box(Modifier.fillMaxWidth().padding(16.dp), contentAlignment = Alignment.Center) {
                                                CircularProgressIndicator(Modifier.size(24.dp).semantics { contentDescription = description })
                                            }
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun DayHeader(label: String) {
    Surface(color = MaterialTheme.colorScheme.surface, modifier = Modifier.fillMaxWidth()) {
        Text(
            label,
            style = MaterialTheme.typography.titleSmall,
            color = MaterialTheme.colorScheme.primary,
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp).semantics { heading() },
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class, ExperimentalFoundationApi::class)
@Composable
private fun LazyItemScope.HistoryRow(row: HistoryRowModel, onAction: (HistoryAction) -> Unit, onDeleteSwiped: () -> Unit) {
    var menuOpen by remember { mutableStateOf(false) }
    // Never actually dismisses: the swipe only asks for confirmation, then the row snaps back.
    val dismissState = rememberSwipeToDismissBoxState(
        confirmValueChange = { value ->
            if (value != SwipeToDismissBoxValue.Settled) onDeleteSwiped()
            false
        },
    )
    val actionLabels = row.actions.associateWith { stringResource(it.labelRes) }

    SwipeToDismissBox(
        state = dismissState,
        enableDismissFromStartToEnd = false,
        modifier = Modifier.animateItem(),
        backgroundContent = {
            if (dismissState.dismissDirection != SwipeToDismissBoxValue.Settled) {
                Box(
                    Modifier.fillMaxSize().background(MaterialTheme.colorScheme.errorContainer).padding(horizontal = 20.dp),
                    contentAlignment = Alignment.CenterEnd,
                ) {
                    Icon(
                        Icons.Outlined.Delete,
                        contentDescription = stringResource(R.string.history_delete),
                        tint = MaterialTheme.colorScheme.onErrorContainer,
                    )
                }
            }
        },
    ) {
        Surface(color = MaterialTheme.colorScheme.surface) {
            Box {
                Row(
                    Modifier
                        .fillMaxWidth()
                        .combinedClickable(
                            onClickLabel = stringResource(R.string.history_row_actions),
                            onLongClickLabel = stringResource(R.string.history_row_actions),
                            role = Role.Button,
                            onClick = { menuOpen = true },
                            onLongClick = { menuOpen = true },
                        )
                        .semantics(mergeDescendants = true) {
                            customActions = row.actions.map { action ->
                                CustomAccessibilityAction(actionLabels.getValue(action)) { onAction(action); true }
                            }
                        }
                        .padding(horizontal = 16.dp, vertical = 12.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(12.dp),
                ) {
                    Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                        Text(row.heading, style = MaterialTheme.typography.titleMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                        Text(
                            row.statusLine,
                            style = MaterialTheme.typography.bodyMedium,
                            color = if (row.status == JobStatus.FAILED) {
                                MaterialTheme.colorScheme.error
                            } else {
                                MaterialTheme.colorScheme.onSurfaceVariant
                            },
                            maxLines = 2,
                            overflow = TextOverflow.Ellipsis,
                        )
                        Text(
                            row.categoryLabel,
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                        )
                    }
                    if (row.hasWarnings) {
                        Icon(
                            Icons.Outlined.Warning,
                            contentDescription = stringResource(R.string.history_cd_warning),
                            tint = MaterialTheme.colorScheme.tertiary,
                        )
                    }
                }
                DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                    for (action in row.actions) {
                        DropdownMenuItem(
                            text = { Text(actionLabels.getValue(action)) },
                            leadingIcon = { Icon(action.icon, contentDescription = null) },
                            onClick = { menuOpen = false; onAction(action) },
                        )
                    }
                }
            }
        }
    }
}

private val HistoryAction.labelRes: Int
    get() = when (this) {
        HistoryAction.CopyPath -> R.string.history_copy_path
        HistoryAction.Retry -> R.string.history_retry
        HistoryAction.Details -> R.string.history_details
        HistoryAction.Delete -> R.string.history_delete
    }

private val HistoryAction.icon: ImageVector
    get() = when (this) {
        HistoryAction.CopyPath -> Icons.Outlined.ContentCopy
        HistoryAction.Retry -> Icons.Outlined.Replay
        HistoryAction.Details -> Icons.Outlined.Info
        HistoryAction.Delete -> Icons.Outlined.Delete
    }

@Composable
private fun DeleteDialog(
    dialog: HistoryDialog.Delete,
    onDeleteFileChange: (Boolean) -> Unit,
    onConfirm: () -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.history_delete_title)) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text(stringResource(R.string.history_delete_body, dialog.name))
                if (dialog.canDeleteFile) {
                    Row(
                        Modifier.fillMaxWidth().toggleable(
                            value = dialog.deleteFile,
                            role = Role.Checkbox,
                            onValueChange = onDeleteFileChange,
                        ),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Checkbox(checked = dialog.deleteFile, onCheckedChange = null)
                        Text(stringResource(R.string.history_delete_file), Modifier.padding(start = 8.dp))
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = onConfirm) { Text(stringResource(R.string.history_delete)) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.history_dialog_cancel)) } },
    )
}

@Composable
private fun ConfirmDialog(title: Int, body: String, confirm: Int, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(title)) },
        text = { Text(body) },
        confirmButton = { TextButton(onClick = onConfirm) { Text(stringResource(confirm)) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.history_dialog_cancel)) } },
    )
}

@Composable
private fun SetupState(onOpenSettings: () -> Unit) {
    Column(
        modifier = Modifier.fillMaxSize().padding(horizontal = 32.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        StateText(Icons.Outlined.CloudOff, stringResource(R.string.queue_setup_title), stringResource(R.string.queue_setup_body))
        Button(onClick = onOpenSettings) { Text(stringResource(R.string.queue_setup_action)) }
    }
}

@Composable
private fun LazyItemScope.EmptyHistory() {
    CenteredItem {
        StateText(Icons.Outlined.History, stringResource(R.string.history_empty_title), stringResource(R.string.history_empty_body))
    }
}

@Composable
private fun LazyItemScope.LoadingState() {
    CenteredItem { CircularProgressIndicator() }
}

@Composable
private fun LazyItemScope.ErrorState(message: String, onRetry: () -> Unit) {
    CenteredItem {
        StateText(Icons.Outlined.ErrorOutline, stringResource(R.string.history_error_title), message)
        TextButton(onClick = onRetry) { Text(stringResource(R.string.history_error_retry)) }
    }
}

@Composable
private fun LazyItemScope.CenteredItem(content: @Composable () -> Unit) {
    Column(
        modifier = Modifier.fillParentMaxSize().padding(horizontal = 32.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) { content() }
}

@Composable
private fun StateText(icon: ImageVector, title: String, body: String) {
    Icon(icon, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
    Text(title, style = MaterialTheme.typography.titleMedium)
    Text(
        body,
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        textAlign = TextAlign.Center,
    )
}
