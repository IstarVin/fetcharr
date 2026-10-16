package com.istarvin.fetcharr.ui.queue

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
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
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
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
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.istarvin.fetcharr.R
import com.istarvin.fetcharr.data.network.dto.JobStatus
import com.istarvin.fetcharr.ui.LocalSnackbarHostState
import com.istarvin.fetcharr.ui.common.ScreenFrame
import com.istarvin.fetcharr.ui.theme.FetcharrTheme
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
    var expandedBatchIds by rememberSaveable { mutableStateOf(listOf<String>()) }
    Scaffold(
        topBar = { TopAppBar(title = { Text(stringResource(R.string.nav_queue)) }) },
        floatingActionButton = {
            FloatingActionButton(onClick = onAdd) {
                Icon(Icons.Outlined.Add, contentDescription = stringResource(R.string.queue_add))
            }
        },
    ) { padding ->
        ScreenFrame(modifier = Modifier.padding(padding), maxWidth = 840.dp) {
            BoxWithConstraints(Modifier.fillMaxSize()) {
                val gutter = if (maxWidth >= 600.dp) 24.dp else 16.dp
                Column(Modifier.fillMaxSize()) {
                    state.banner?.let { ConnectionBanner(it) }
                    when {
                        !state.loaded -> CenteredColumn { LoadingFeedback() }
                        !state.hasServer -> SetupState(onOpenSettings)
                        else -> PullToRefreshBox(
                            isRefreshing = state.isRefreshing,
                            onRefresh = onRefresh,
                            modifier = Modifier.fillMaxSize(),
                        ) {
                            LazyColumn(
                                modifier = Modifier.fillMaxSize(),
                                // Room below the last card for the FAB.
                                contentPadding = PaddingValues(start = gutter, end = gutter, top = 12.dp, bottom = 96.dp),
                                verticalArrangement = Arrangement.spacedBy(12.dp),
                            ) {
                                if (state.sections.isEmpty()) {
                                    item(key = "empty") { EmptyQueue(state) }
                                } else {
                                    for (section in state.sections) {
                                        item(key = "header:${section.kind.name}") { SectionHeader(section.kind) }
                                        for (item in section.items) {
                                            when (item) {
                                                is JobItem -> item(key = item.key, contentType = "job") {
                                                    JobCard(
                                                        job = item.job,
                                                        onPause = { onPause(item.job.id) },
                                                        onResume = { onResume(item.job.id) },
                                                        onCancel = { onRequestCancel(item.job) },
                                                    )
                                                }
                                                is BatchGroup -> {
                                                    val expanded = item.batchId in expandedBatchIds
                                                    item(key = item.key, contentType = "batch") {
                                                        BatchGroupCard(
                                                            group = item,
                                                            expanded = expanded,
                                                            onToggleExpanded = {
                                                                expandedBatchIds = if (expanded) {
                                                                    expandedBatchIds - item.batchId
                                                                } else {
                                                                    expandedBatchIds + item.batchId
                                                                }
                                                            },
                                                            onPauseAll = { onPauseAll(item.batchId) },
                                                            onResumeAll = { onResumeAll(item.batchId) },
                                                            onCancelAll = { onRequestCancelAll(item) },
                                                        )
                                                    }
                                                    if (expanded) {
                                                        items(item.jobs, key = { "${item.key}:job:${it.id}" }, contentType = { "batch-job" }) { job ->
                                                            Card(
                                                                modifier = Modifier.fillMaxWidth().padding(start = 12.dp),
                                                                shape = RoundedCornerShape(16.dp),
                                                            ) {
                                                                JobDetails(
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
            state.connecting -> {
                LoadingFeedback()
            }
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
) {
    Card(modifier = Modifier.fillMaxWidth(), shape = RoundedCornerShape(16.dp)) {
        JobDetails(job, onPause, onResume, onCancel)
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun JobDetails(
    job: JobCardModel,
    onPause: () -> Unit,
    onResume: () -> Unit,
    onCancel: () -> Unit,
    showCategory: Boolean = true,
) {
    Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(job.heading, style = MaterialTheme.typography.titleMedium)
        job.secondary?.let {
            Text(it, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        FlowRow(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
            itemVerticalAlignment = Alignment.CenterVertically,
        ) {
            if (showCategory) {
                Text(job.categoryLabel, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            StatusBadge(job)
        }
        if (job.showsProgressBar) ProgressRow(job.progress, job.indeterminate, job.percentLabel)
        job.stats?.let {
            Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        if (job.actions.isNotEmpty()) {
            FlowRow(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(8.dp, Alignment.End),
            ) {
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

@Composable
private fun StatusBadge(job: JobCardModel) {
    val active = job.status == JobStatus.DOWNLOADING || job.status == JobStatus.PROCESSING
    Surface(
        color = if (active) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surfaceContainerHighest,
        contentColor = if (active) MaterialTheme.colorScheme.onPrimaryContainer else MaterialTheme.colorScheme.onSurfaceVariant,
        shape = RoundedCornerShape(8.dp),
    ) {
        Text(job.statusLabel, style = MaterialTheme.typography.labelMedium, modifier = Modifier.padding(horizontal = 8.dp, vertical = 4.dp))
    }
}

@Composable
private fun LoadingFeedback() {
    CircularProgressIndicator(Modifier.size(32.dp))
    Text(stringResource(R.string.a11y_loading), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
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
    expanded: Boolean,
    onToggleExpanded: () -> Unit,
    onPauseAll: () -> Unit,
    onResumeAll: () -> Unit,
    onCancelAll: () -> Unit,
) {
    var menuOpen by remember { mutableStateOf(false) }
    val toggleLabel = stringResource(if (expanded) R.string.queue_batch_collapse else R.string.queue_batch_expand, group.title)
    val canPause = group.jobs.any { JobAction.Pause in it.actions }
    val canResume = group.jobs.any { JobAction.Resume in it.actions }

    Card(modifier = Modifier.fillMaxWidth(), shape = RoundedCornerShape(16.dp)) {
        Column(Modifier.padding(vertical = 8.dp)) {
            Row(
                Modifier.fillMaxWidth().clickable(onClickLabel = toggleLabel, onClick = onToggleExpanded).padding(start = 16.dp, end = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column(Modifier.weight(1f).padding(vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text(group.title, style = MaterialTheme.typography.titleMedium)
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

private fun previewQueueJob(id: String = "preview", status: JobStatus = JobStatus.DOWNLOADING) = JobCardModel(
    id = id,
    batchId = null,
    status = status,
    categoryLabel = "Television",
    statusLabel = if (status == JobStatus.PAUSED) "Paused" else "Downloading",
    heading = "A very long television series title — S02E03",
    secondary = "An episode title that wraps comfortably at larger text sizes",
    progress = 0.64f,
    indeterminate = false,
    percentLabel = "64%",
    stats = "3.1 MiB/s · 1:52 left · 463 MiB / 720 MiB",
    actions = if (status == JobStatus.PAUSED) listOf(JobAction.Resume, JobAction.Cancel) else listOf(JobAction.Pause, JobAction.Cancel),
)

@Composable
private fun QueuePreview(state: QueueUiState) {
    FetcharrTheme {
        QueueContent(state, {}, {}, {}, {}, {}, {}, {}, {}, {})
    }
}

@Preview(name = "Queue · populated", showBackground = true)
@Preview(name = "Queue · large text", showBackground = true, fontScale = 1.5f)
@Composable
private fun PopulatedQueuePreview() {
    QueuePreview(QueueUiState(loaded = true, hasServer = true, hasSnapshot = true, sections = listOf(
        QueueSection(QueueSectionKind.Active, listOf(JobItem(previewQueueJob(), QueueSectionKind.Active))),
        QueueSection(QueueSectionKind.Paused, listOf(JobItem(previewQueueJob("paused", JobStatus.PAUSED), QueueSectionKind.Paused))),
    )))
}

@Preview(name = "Queue · expanded batch", showBackground = true, heightDp = 600)
@Composable
private fun ExpandedBatchPreview() {
    val group = BatchGroup("batch", QueueSectionKind.Active, "A television series — Season 2", listOf(previewQueueJob("one"), previewQueueJob("two")), 0.64f)
    FetcharrTheme {
        Surface {
            LazyColumn(
                contentPadding = PaddingValues(16.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                item {
                    BatchGroupCard(
                        group = group, expanded = true, onToggleExpanded = {},
                        onPauseAll = {}, onResumeAll = {}, onCancelAll = {},
                    )
                }
                items(group.jobs, key = { it.id }) { job ->
                    Card(Modifier.fillMaxWidth().padding(start = 12.dp), shape = RoundedCornerShape(16.dp)) {
                        JobDetails(job, onPause = {}, onResume = {}, onCancel = {}, showCategory = false)
                    }
                }
            }
        }
    }
}

@Preview(name = "Queue · setup", showBackground = true)
@Composable
private fun SetupQueuePreview() { QueuePreview(QueueUiState(loaded = true)) }

@Preview(name = "Queue · unavailable", showBackground = true)
@Composable
private fun UnavailableQueuePreview() {
    QueuePreview(QueueUiState(loaded = true, hasServer = true, banner = QueueBanner("Unable to connect to the server", false)))
}
