package dev.istarvin.fetcharr.ui.queue

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyItemScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.CloudOff
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.ExpandLess
import androidx.compose.material.icons.outlined.ExpandMore
import androidx.compose.material.icons.outlined.HourglassEmpty
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarResult
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.istarvin.fetcharr.R
import dev.istarvin.fetcharr.ui.LocalSnackbarHostState
import kotlinx.coroutines.launch

@Composable
fun QueueScreen(onOpenSettings: () -> Unit, onAdd: () -> Unit, viewModel: QueueViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val snackbar = LocalSnackbarHostState.current
    val settingsLabel = stringResource(R.string.queue_snackbar_settings)

    LaunchedEffect(viewModel) {
        viewModel.events.collect { event ->
            when (event) {
                is QueueEvent.Snackbar -> {
                    snackbar.currentSnackbarData?.dismiss()
                    launch {
                        val result = snackbar.showSnackbar(
                            message = event.message,
                            actionLabel = if (event.openSettings) settingsLabel else null,
                        )
                        if (result == SnackbarResult.ActionPerformed) onOpenSettings()
                    }
                }
            }
        }
    }

    QueueContent(
        state = state,
        onOpenSettings = onOpenSettings,
        onAdd = onAdd,
        onRefresh = viewModel::refresh,
        onPause = viewModel::pause,
        onResume = viewModel::resume,
        onRequestCancel = viewModel::requestCancel,
        onPauseAll = viewModel::pauseAll,
        onResumeAll = viewModel::resumeAll,
        onRequestCancelAll = viewModel::requestCancelAll,
    )

    state.pendingCancel?.let { request ->
        CancelDialog(request, onConfirm = viewModel::confirmCancel, onDismiss = viewModel::dismissCancel)
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun QueueContent(
    state: QueueUiState,
    onOpenSettings: () -> Unit,
    onAdd: () -> Unit,
    onRefresh: () -> Unit,
    onPause: (String) -> Unit,
    onResume: (String) -> Unit,
    onRequestCancel: (JobCardModel) -> Unit,
    onPauseAll: (String) -> Unit,
    onResumeAll: (String) -> Unit,
    onRequestCancelAll: (BatchGroup) -> Unit,
) {
    Scaffold(
        topBar = { TopAppBar(title = { Text(stringResource(R.string.nav_queue)) }) },
        floatingActionButton = {
            FloatingActionButton(onClick = onAdd) {
                Icon(Icons.Outlined.Add, contentDescription = stringResource(R.string.queue_add))
            }
        },
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            state.banner?.let { ConnectionBanner(it) }
            when {
                !state.loaded -> Unit
                !state.hasServer -> SetupState(onOpenSettings)
                else -> PullToRefreshBox(
                    isRefreshing = state.isRefreshing,
                    onRefresh = onRefresh,
                    modifier = Modifier.fillMaxSize(),
                ) {
                    LazyColumn(
                        modifier = Modifier.fillMaxSize(),
                        // Room below the last card for the FAB.
                        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 8.dp, bottom = 96.dp),
                        verticalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        if (state.sections.isEmpty()) {
                            item(key = "empty") { EmptyQueue(state) }
                        } else {
                            for (section in state.sections) {
                                item(key = "header:${section.kind.name}") { SectionHeader(section.kind) }
                                items(section.items, key = { it.key }) { item ->
                                    when (item) {
                                        is JobItem -> JobCard(
                                            job = item.job,
                                            onPause = { onPause(item.job.id) },
                                            onResume = { onResume(item.job.id) },
                                            onCancel = { onRequestCancel(item.job) },
                                        )
                                        is BatchGroup -> BatchGroupCard(
                                            group = item,
                                            onPause = onPause,
                                            onResume = onResume,
                                            onRequestCancel = onRequestCancel,
                                            onPauseAll = { onPauseAll(item.batchId) },
                                            onResumeAll = { onResumeAll(item.batchId) },
                                            onCancelAll = { onRequestCancelAll(item) },
                                        )
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
private fun ConnectionBanner(banner: QueueBanner) {
    val description = stringResource(R.string.queue_cd_banner, banner.text)
    Surface(
        color = if (banner.connecting) MaterialTheme.colorScheme.secondaryContainer else MaterialTheme.colorScheme.errorContainer,
        contentColor = if (banner.connecting) {
            MaterialTheme.colorScheme.onSecondaryContainer
        } else {
            MaterialTheme.colorScheme.onErrorContainer
        },
        modifier = Modifier
            .fillMaxWidth()
            .clearAndSetSemantics {
                contentDescription = description
                liveRegion = LiveRegionMode.Polite
            },
    ) {
        Row(
            Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            if (banner.connecting) CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
            Text(banner.text, style = MaterialTheme.typography.labelLarge)
        }
    }
}

@Composable
private fun SetupState(onOpenSettings: () -> Unit) {
    CenteredColumn {
        EmptyState(Icons.Outlined.CloudOff, R.string.queue_setup_title, R.string.queue_setup_body)
        Button(onClick = onOpenSettings) { Text(stringResource(R.string.queue_setup_action)) }
    }
}

@Composable
private fun LazyItemScope.EmptyQueue(state: QueueUiState) {
    Column(
        modifier = Modifier.fillParentMaxSize().padding(horizontal = 16.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        when {
            state.hasSnapshot -> EmptyState(Icons.Outlined.Download, R.string.queue_empty_title, R.string.queue_empty_body)
            state.connecting -> CircularProgressIndicator()
            else -> EmptyState(Icons.Outlined.HourglassEmpty, R.string.queue_unavailable_title, R.string.queue_unavailable_body)
        }
    }
}

@Composable
private fun CenteredColumn(content: @Composable () -> Unit) {
    Column(
        modifier = Modifier.fillMaxSize().padding(horizontal = 32.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) { content() }
}

@Composable
private fun EmptyState(icon: ImageVector, title: Int, body: Int) {
    Icon(icon, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
    Text(stringResource(title), style = MaterialTheme.typography.titleMedium)
    Text(
        stringResource(body),
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        textAlign = TextAlign.Center,
    )
}

@Composable
private fun SectionHeader(kind: QueueSectionKind) {
    val label = stringResource(
        when (kind) {
            QueueSectionKind.Active -> R.string.queue_section_active
            QueueSectionKind.Queued -> R.string.queue_section_queued
            QueueSectionKind.Paused -> R.string.queue_section_paused
        },
    )
    Text(
        label,
        style = MaterialTheme.typography.titleSmall,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(top = 8.dp).semantics { heading() },
    )
}

@Composable
private fun JobCard(
    job: JobCardModel,
    onPause: () -> Unit,
    onResume: () -> Unit,
    onCancel: () -> Unit,
    showCategory: Boolean = true,
) {
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                if (showCategory) Pill(job.categoryLabel, MaterialTheme.colorScheme.secondaryContainer)
                Spacer(Modifier.weight(1f))
                Pill(job.statusLabel, MaterialTheme.colorScheme.tertiaryContainer)
            }
            Text(job.heading, style = MaterialTheme.typography.titleMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
            job.secondary?.let {
                Text(
                    it,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            if (job.showsProgressBar) {
                ProgressRow(job.progress, job.indeterminate, job.percentLabel)
            }
            job.stats?.let {
                Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            if (job.actions.isNotEmpty()) {
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                    for (action in job.actions) {
                        when (action) {
                            JobAction.Pause -> ActionButton(R.string.queue_action_pause, R.string.queue_cd_pause, job.heading, onPause)
                            JobAction.Resume -> ActionButton(R.string.queue_action_resume, R.string.queue_cd_resume, job.heading, onResume)
                            JobAction.Cancel -> ActionButton(R.string.queue_action_cancel, R.string.queue_cd_cancel, job.heading, onCancel)
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun Pill(text: String, color: androidx.compose.ui.graphics.Color) {
    Surface(color = color, shape = RoundedCornerShape(50)) {
        Text(
            text,
            style = MaterialTheme.typography.labelMedium,
            modifier = Modifier.padding(horizontal = 10.dp, vertical = 2.dp),
            maxLines = 1,
        )
    }
}

@Composable
private fun ActionButton(label: Int, description: Int, name: String, onClick: () -> Unit) {
    val cd = stringResource(description, name)
    TextButton(onClick = onClick, modifier = Modifier.semantics { contentDescription = cd }) {
        Text(stringResource(label))
    }
}

/** Determinate bars animate between updates; an unknown [progress] with [indeterminate] shows the moving bar. */
@Composable
private fun ProgressRow(progress: Float?, indeterminate: Boolean, percentLabel: String?) {
    val description = if (indeterminate || progress == null) {
        stringResource(R.string.queue_cd_finishing)
    } else {
        stringResource(R.string.queue_cd_progress, percentInt(progress))
    }
    Row(
        Modifier.fillMaxWidth().clearAndSetSemantics { contentDescription = description },
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        if (indeterminate || progress == null) {
            LinearProgressIndicator(Modifier.weight(1f))
        } else {
            val animated by animateFloatAsState(progress, animationSpec = tween(durationMillis = 500), label = "progress")
            LinearProgressIndicator(progress = { animated }, modifier = Modifier.weight(1f))
            percentLabel?.let { Text(it, style = MaterialTheme.typography.labelMedium) }
        }
    }
}

@Composable
private fun BatchGroupCard(
    group: BatchGroup,
    onPause: (String) -> Unit,
    onResume: (String) -> Unit,
    onRequestCancel: (JobCardModel) -> Unit,
    onPauseAll: () -> Unit,
    onResumeAll: () -> Unit,
    onCancelAll: () -> Unit,
) {
    var expanded by rememberSaveable(group.key) { mutableStateOf(false) }
    var menuOpen by remember { mutableStateOf(false) }
    val toggleLabel = stringResource(if (expanded) R.string.queue_batch_collapse else R.string.queue_batch_expand, group.title)
    val canPause = group.jobs.any { JobAction.Pause in it.actions }
    val canResume = group.jobs.any { JobAction.Resume in it.actions }

    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(vertical = 8.dp)) {
            Row(
                Modifier.fillMaxWidth().clickable(onClickLabel = toggleLabel) { expanded = !expanded }.padding(start = 16.dp, end = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column(Modifier.weight(1f).padding(vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text(group.title, style = MaterialTheme.typography.titleMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    Text(
                        pluralStringResource(R.plurals.queue_episode_count, group.episodeCount, group.episodeCount),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    group.progress?.let { ProgressRow(it, indeterminate = false, percentLabel = group.percentLabel) }
                }
                Icon(
                    if (expanded) Icons.Outlined.ExpandLess else Icons.Outlined.ExpandMore,
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Box {
                    IconButton(onClick = { menuOpen = true }) {
                        Icon(Icons.Outlined.MoreVert, contentDescription = stringResource(R.string.queue_batch_menu, group.title))
                    }
                    DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                        if (canPause) {
                            DropdownMenuItem(
                                text = { Text(stringResource(R.string.queue_batch_pause_all)) },
                                onClick = { menuOpen = false; onPauseAll() },
                            )
                        }
                        if (canResume) {
                            DropdownMenuItem(
                                text = { Text(stringResource(R.string.queue_batch_resume_all)) },
                                onClick = { menuOpen = false; onResumeAll() },
                            )
                        }
                        DropdownMenuItem(
                            text = { Text(stringResource(R.string.queue_batch_cancel_all)) },
                            onClick = { menuOpen = false; onCancelAll() },
                        )
                    }
                }
            }
            if (expanded) {
                Column(Modifier.padding(horizontal = 8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    for (job in group.jobs) {
                        JobCard(
                            job = job,
                            onPause = { onPause(job.id) },
                            onResume = { onResume(job.id) },
                            onCancel = { onRequestCancel(job) },
                            showCategory = false,
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun CancelDialog(request: CancelRequest, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    val (title, body) = when (request) {
        is CancelRequest.SingleJob ->
            stringResource(R.string.queue_cancel_title) to stringResource(R.string.queue_cancel_body, request.name)
        is CancelRequest.WholeBatch ->
            stringResource(R.string.queue_cancel_all_title, request.name) to stringResource(R.string.queue_cancel_all_body)
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { Text(body) },
        confirmButton = { TextButton(onClick = onConfirm) { Text(stringResource(R.string.queue_cancel_confirm)) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.queue_cancel_keep)) } },
    )
}
