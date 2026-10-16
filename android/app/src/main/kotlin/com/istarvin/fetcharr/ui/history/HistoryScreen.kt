package com.istarvin.fetcharr.ui.history

import android.content.ActivityNotFoundException
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.os.Build
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.wrapContentSize
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyItemScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.selection.toggleable
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Cancel
import androidx.compose.material.icons.outlined.CheckCircle
import androidx.compose.material.icons.outlined.CloudOff
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.DeleteSweep
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.ErrorOutline
import androidx.compose.material.icons.outlined.History
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material.icons.outlined.PlayArrow
import androidx.compose.material.icons.outlined.Replay
import androidx.compose.material.icons.outlined.Warning
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.FilledTonalIconButton
import androidx.compose.material3.MediumTopAppBar
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
import androidx.compose.material3.TopAppBarDefaults
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.CustomAccessibilityAction
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.customActions
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.core.net.toUri
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.istarvin.fetcharr.R
import com.istarvin.fetcharr.data.network.ApiError
import com.istarvin.fetcharr.data.network.dto.JobStatus
import com.istarvin.fetcharr.ui.LocalSnackbarHostState
import com.istarvin.fetcharr.ui.common.EmptyState
import com.istarvin.fetcharr.ui.common.IconTile
import com.istarvin.fetcharr.ui.common.LoadingBlock
import com.istarvin.fetcharr.ui.common.Pill
import com.istarvin.fetcharr.ui.common.ScreenFrame
import com.istarvin.fetcharr.ui.theme.FetcharrTheme
import com.istarvin.fetcharr.ui.theme.statusColors
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
    val noPlayerMessage = stringResource(R.string.history_no_player)
    val playerFailedMessage = stringResource(R.string.history_player_failed)

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
                is HistoryEvent.OpenPlayer -> {
                    if (viewModel.isCurrentPlayback(event)) {
                        val intent = Intent(Intent.ACTION_VIEW).apply {
                            setDataAndType(event.url.toUri(), event.mimeType)
                            putExtra(Intent.EXTRA_TITLE, event.title)
                        }
                        val error = try {
                            context.startActivity(intent)
                            null
                        } catch (_: ActivityNotFoundException) {
                            noPlayerMessage
                        } catch (_: SecurityException) {
                            playerFailedMessage
                        }
                        if (error != null) {
                            snackbar.currentSnackbarData?.dismiss()
                            launch { snackbar.showSnackbar(error) }
                        }
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
                HistoryAction.Play -> viewModel.play(id)
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
    val scrollBehavior = TopAppBarDefaults.exitUntilCollapsedScrollBehavior()
    Scaffold(
        modifier = Modifier.nestedScroll(scrollBehavior.nestedScrollConnection),
        topBar = {
            MediumTopAppBar(
                title = { Text(stringResource(R.string.nav_history)) },
                scrollBehavior = scrollBehavior,
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = MaterialTheme.colorScheme.surface,
                    scrolledContainerColor = MaterialTheme.colorScheme.surfaceContainer,
                ),
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
                                    leadingIcon = { Icon(Icons.Outlined.DeleteSweep, contentDescription = null) },
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
        ScreenFrame(modifier = Modifier.padding(padding), maxWidth = 840.dp) {
            BoxWithConstraints(Modifier.fillMaxSize()) {
                val gutter = if (maxWidth >= 600.dp) 24.dp else 16.dp
                when {
                    !state.loaded -> LoadingBlock(Modifier.fillMaxSize().wrapContentSize(Alignment.Center))
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
                                contentPadding = PaddingValues(start = gutter, end = gutter, bottom = 24.dp),
                                verticalArrangement = Arrangement.spacedBy(8.dp),
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
                                                HistoryRow(row, row.id in state.preparingPlayback, onAction = { onAction(it, row.id) }, onDeleteSwiped = { onDeleteSwiped(row.id) })
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
}

@Composable
private fun DayHeader(label: String) {
    Surface(color = MaterialTheme.colorScheme.surface, modifier = Modifier.fillMaxWidth()) {
        Text(
            label,
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.primary,
            modifier = Modifier.padding(start = 4.dp, top = 16.dp, bottom = 8.dp).semantics { heading() },
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class, ExperimentalFoundationApi::class)
@Composable
private fun LazyItemScope.HistoryRow(row: HistoryRowModel, preparingPlayback: Boolean, onAction: (HistoryAction) -> Unit, onDeleteSwiped: () -> Unit) {
    var menuOpen by remember { mutableStateOf(false) }
    // Never actually dismisses: the swipe only asks for confirmation, then the row snaps back.
    val dismissState = rememberSwipeToDismissBoxState(
        confirmValueChange = { value ->
            if (value != SwipeToDismissBoxValue.Settled) onDeleteSwiped()
            false
        },
    )
    val actionLabels = row.actions.associateWith { stringResource(it.labelRes) }
    val colors = MaterialTheme.colorScheme
    val status = MaterialTheme.statusColors
    val (tileIcon, tileContainer, tileContent) = when (row.status) {
        JobStatus.COMPLETED -> Triple(Icons.Outlined.CheckCircle, status.successContainer, status.onSuccessContainer)
        JobStatus.FAILED -> Triple(Icons.Outlined.ErrorOutline, colors.errorContainer, colors.onErrorContainer)
        else -> Triple(Icons.Outlined.Cancel, colors.surfaceContainerHighest, colors.onSurfaceVariant)
    }
    val shape = MaterialTheme.shapes.large

    SwipeToDismissBox(
        state = dismissState,
        enableDismissFromStartToEnd = false,
        modifier = Modifier.animateItem().clip(shape),
        backgroundContent = {
            if (dismissState.dismissDirection != SwipeToDismissBoxValue.Settled) {
                Box(
                    Modifier.fillMaxSize().background(colors.errorContainer).padding(horizontal = 24.dp),
                    contentAlignment = Alignment.CenterEnd,
                ) {
                    Icon(
                        Icons.Outlined.Delete,
                        contentDescription = stringResource(R.string.history_delete),
                        tint = colors.onErrorContainer,
                    )
                }
            }
        },
    ) {
        Surface(color = colors.surfaceContainerLow, shape = shape) {
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
                        .padding(start = 16.dp, top = 12.dp, bottom = 12.dp, end = 4.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(12.dp),
                ) {
                    IconTile(tileIcon, container = tileContainer, content = tileContent)
                    Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                        Text(row.heading, style = MaterialTheme.typography.titleMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                        Text(
                            row.statusLine,
                            style = MaterialTheme.typography.bodyMedium,
                            color = if (row.status == JobStatus.FAILED) colors.error else colors.onSurfaceVariant,
                            maxLines = if (row.status == JobStatus.COMPLETED) 1 else 2,
                            overflow = TextOverflow.Ellipsis,
                        )
                        Row(
                            horizontalArrangement = Arrangement.spacedBy(8.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Pill(row.categoryLabel, icon = Icons.Outlined.Folder)
                            if (row.hasWarnings) {
                                Pill(
                                    stringResource(R.string.history_cd_warning),
                                    icon = Icons.Outlined.Warning,
                                    container = status.warningContainer,
                                    content = status.onWarningContainer,
                                )
                            }
                        }
                    }
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        if (HistoryAction.Play in row.actions) {
                            FilledTonalIconButton(onClick = { onAction(HistoryAction.Play) }, enabled = !preparingPlayback) {
                                if (preparingPlayback) {
                                    CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp)
                                } else {
                                    Icon(Icons.Outlined.PlayArrow, contentDescription = stringResource(R.string.history_play))
                                }
                            }
                        }
                        IconButton(onClick = { menuOpen = true }) {
                            Icon(Icons.Outlined.MoreVert, contentDescription = stringResource(R.string.history_row_actions))
                        }
                    }
                }
                DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                    for (action in row.actions) {
                        DropdownMenuItem(
                            enabled = action != HistoryAction.Play || !preparingPlayback,
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
        HistoryAction.Play -> R.string.history_play
        HistoryAction.CopyPath -> R.string.history_copy_path
        HistoryAction.Retry -> R.string.history_retry
        HistoryAction.Details -> R.string.history_details
        HistoryAction.Delete -> R.string.history_delete
    }

private val HistoryAction.icon: ImageVector
    get() = when (this) {
        HistoryAction.Play -> Icons.Outlined.PlayArrow
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
    EmptyState(
        icon = Icons.Outlined.CloudOff,
        title = stringResource(R.string.queue_setup_title),
        body = stringResource(R.string.queue_setup_body),
        modifier = Modifier.fillMaxSize().wrapContentSize(Alignment.Center),
        actions = { Button(onClick = onOpenSettings) { Text(stringResource(R.string.queue_setup_action)) } },
    )
}

@Composable
private fun LazyItemScope.EmptyHistory() {
    CenteredItem {
        EmptyState(
            icon = Icons.Outlined.History,
            title = stringResource(R.string.history_empty_title),
            body = stringResource(R.string.history_empty_body),
        )
    }
}

@Composable
private fun LazyItemScope.LoadingState() {
    CenteredItem { LoadingBlock() }
}

@Composable
private fun LazyItemScope.ErrorState(message: String, onRetry: () -> Unit) {
    CenteredItem {
        EmptyState(
            icon = Icons.Outlined.ErrorOutline,
            title = stringResource(R.string.history_error_title),
            body = message,
            tint = MaterialTheme.colorScheme.error,
            container = MaterialTheme.colorScheme.errorContainer.copy(alpha = 0.6f),
            actions = {
                FilledTonalButton(onClick = onRetry) {
                    Icon(Icons.Outlined.Refresh, contentDescription = null, modifier = Modifier.size(18.dp))
                    Text(stringResource(R.string.history_error_retry), Modifier.padding(start = 8.dp))
                }
            },
        )
    }
}

@Composable
private fun LazyItemScope.CenteredItem(content: @Composable () -> Unit) {
    Column(
        modifier = Modifier.fillParentMaxSize(),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) { content() }
}

@Composable
private fun HistoryPreview(state: HistoryUiState) {
    FetcharrTheme { HistoryContent(state, {}, {}, {}, { _, _ -> }, {}, {}) }
}

@Preview(name = "History · populated", showBackground = true)
@Preview(name = "History · large text", showBackground = true, fontScale = 1.5f)
@Preview(name = "History · tablet", showBackground = true, widthDp = 900)
@Composable
private fun PopulatedHistoryPreview() {
    val completed = HistoryRowModel(
        id = "completed", status = JobStatus.COMPLETED, finishedAt = 0L, categoryLabel = "Movies",
        heading = "A long movie title with a descriptive subtitle (2026)",
        statusLine = "/media/movies/A long movie title (2026)/A long movie title.mkv",
        hasWarnings = true, warnings = listOf("Some metadata could not be saved"),
        filePath = "/media/movies/title.mkv", errorCode = null, errorMessage = null,
        actions = listOf(HistoryAction.Play, HistoryAction.CopyPath, HistoryAction.Details, HistoryAction.Delete),
    )
    val failed = completed.copy(
        id = "failed", status = JobStatus.FAILED, heading = "A television series — S02E03",
        statusLine = "Not enough disk space on server", hasWarnings = false, warnings = emptyList(),
        filePath = null, errorCode = "low_disk_space", errorMessage = "Not enough disk space on server",
        actions = listOf(HistoryAction.Retry, HistoryAction.Details, HistoryAction.Delete),
    )
    HistoryPreview(HistoryUiState(loaded = true, hasServer = true, historyLoaded = true, groups = listOf(
        HistoryGroup("today", "Today", listOf(completed)), HistoryGroup("yesterday", "Yesterday", listOf(failed)),
    )))
}

@Preview(name = "History · error", showBackground = true)
@Composable
private fun ErrorHistoryPreview() {
    HistoryPreview(HistoryUiState(loaded = true, hasServer = true, error = ApiError.Server(503)))
}

@Preview(name = "History · setup", showBackground = true)
@Composable
private fun SetupHistoryPreview() { HistoryPreview(HistoryUiState(loaded = true)) }
