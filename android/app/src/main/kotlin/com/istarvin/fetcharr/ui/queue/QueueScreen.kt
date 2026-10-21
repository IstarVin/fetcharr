package com.istarvin.fetcharr.ui.queue

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
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
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyItemScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.Autorenew
import androidx.compose.material.icons.outlined.CloudOff
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.ExpandMore
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.HourglassEmpty
import androidx.compose.material.icons.outlined.Layers
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material.icons.outlined.Movie
import androidx.compose.material.icons.outlined.Pause
import androidx.compose.material.icons.outlined.PauseCircle
import androidx.compose.material.icons.outlined.PlayArrow
import androidx.compose.material.icons.outlined.Schedule
import androidx.compose.material.icons.outlined.Speed
import androidx.compose.material.icons.outlined.Storage
import androidx.compose.material.icons.outlined.Timer
import androidx.compose.material.icons.outlined.Tv
import androidx.compose.material.icons.outlined.WifiOff
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.FilledTonalIconButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MediumTopAppBar
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarResult
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBarDefaults
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.nestedscroll.nestedScroll
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
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.istarvin.fetcharr.R
import com.istarvin.fetcharr.data.network.dto.JobStatus
import com.istarvin.fetcharr.ui.LocalSnackbarHostState
import com.istarvin.fetcharr.ui.common.AppCard
import com.istarvin.fetcharr.ui.common.EmptyState
import com.istarvin.fetcharr.ui.common.IconTile
import com.istarvin.fetcharr.ui.common.LoadingBlock
import com.istarvin.fetcharr.ui.common.Pill
import com.istarvin.fetcharr.ui.common.ScreenFrame
import com.istarvin.fetcharr.ui.theme.FetcharrTheme
import com.istarvin.fetcharr.ui.theme.statusColors
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
    val scrollBehavior = TopAppBarDefaults.exitUntilCollapsedScrollBehavior()
    val listState = rememberLazyListState()
    Scaffold(
        modifier = Modifier.nestedScroll(scrollBehavior.nestedScrollConnection),
        topBar = {
            MediumTopAppBar(
                title = { Text(stringResource(R.string.nav_queue)) },
                scrollBehavior = scrollBehavior,
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = MaterialTheme.colorScheme.surface,
                    scrolledContainerColor = MaterialTheme.colorScheme.surfaceContainer,
                ),
            )
        },
        floatingActionButton = {
            if (state.hasServer) {
                ExtendedFloatingActionButton(
                    onClick = onAdd,
                    expanded = !listState.canScrollBackward,
                    icon = { Icon(Icons.Outlined.Add, contentDescription = null) },
                    text = { Text(stringResource(R.string.queue_add)) },
                )
            }
        },
    ) { padding ->
        ScreenFrame(modifier = Modifier.padding(padding), maxWidth = 840.dp) {
            BoxWithConstraints(Modifier.fillMaxSize()) {
                val gutter = if (maxWidth >= 600.dp) 24.dp else 16.dp
                Column(Modifier.fillMaxSize()) {
                    state.banner?.let { ConnectionBanner(it) }
                    when {
                        !state.loaded -> CenteredColumn { LoadingBlock() }
                        !state.hasServer -> SetupState(onOpenSettings)
                        else -> PullToRefreshBox(
                            isRefreshing = state.isRefreshing,
                            onRefresh = onRefresh,
                            modifier = Modifier.fillMaxSize(),
                        ) {
                            LazyColumn(
                                state = listState,
                                modifier = Modifier.fillMaxSize(),
                                // Room below the last card for the FAB.
                                contentPadding = PaddingValues(start = gutter, end = gutter, top = 4.dp, bottom = 104.dp),
                                verticalArrangement = Arrangement.spacedBy(12.dp),
                            ) {
                                if (state.sections.isEmpty()) {
                                    item(key = "empty") { EmptyQueue(state) }
                                } else {
                                    for (section in state.sections) {
                                        item(key = "header:${section.kind.name}") {
                                            SectionHeader(section.kind, section.items.sumOf { it.jobCount }, section.speedLabel, Modifier.animateItem())
                                        }
                                        for (item in section.items) {
                                            when (item) {
                                                is JobItem -> item(key = item.key, contentType = "job") {
                                                    JobCard(
                                                        job = item.job,
                                                        onPause = { onPause(item.job.id) },
                                                        onResume = { onResume(item.job.id) },
                                                        onCancel = { onRequestCancel(item.job) },
                                                        modifier = Modifier.animateItem(),
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
                                                            modifier = Modifier.animateItem(),
                                                        )
                                                    }
                                                    if (expanded) {
                                                        items(item.jobs, key = { "${item.key}:job:${it.id}" }, contentType = { "batch-job" }) { job ->
                                                            AppCard(
                                                                modifier = Modifier.fillMaxWidth().padding(start = 16.dp).animateItem(),
                                                                color = MaterialTheme.colorScheme.surfaceContainer,
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

private val QueueItem.jobCount: Int
    get() = when (this) {
        is JobItem -> 1
        is BatchGroup -> jobs.size
    }

@Composable
private fun ConnectionBanner(banner: QueueBanner) {
    val description = stringResource(R.string.queue_cd_banner, banner.text)
    val colors = MaterialTheme.colorScheme
    Surface(
        color = if (banner.connecting) colors.secondaryContainer else colors.errorContainer,
        contentColor = if (banner.connecting) colors.onSecondaryContainer else colors.onErrorContainer,
        shape = MaterialTheme.shapes.medium,
        modifier = Modifier
            .padding(horizontal = 16.dp, vertical = 4.dp)
            .fillMaxWidth()
            .clearAndSetSemantics {
                contentDescription = description
                liveRegion = LiveRegionMode.Polite
            },
    ) {
        Row(
            Modifier.padding(horizontal = 16.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            if (banner.connecting) {
                CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
            } else {
                Icon(Icons.Outlined.WifiOff, contentDescription = null, modifier = Modifier.size(18.dp))
            }
            Text(banner.text, style = MaterialTheme.typography.labelLarge)
        }
    }
}

@Composable
private fun SetupState(onOpenSettings: () -> Unit) {
    CenteredColumn {
        EmptyState(
            icon = Icons.Outlined.CloudOff,
            title = stringResource(R.string.queue_setup_title),
            body = stringResource(R.string.queue_setup_body),
            actions = { Button(onClick = onOpenSettings) { Text(stringResource(R.string.queue_setup_action)) } },
        )
    }
}

@Composable
private fun LazyItemScope.EmptyQueue(state: QueueUiState) {
    Column(
        modifier = Modifier.fillParentMaxSize(),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        when {
            state.hasSnapshot -> EmptyState(
                icon = Icons.Outlined.Download,
                title = stringResource(R.string.queue_empty_title),
                body = stringResource(R.string.queue_empty_body),
            )
            state.connecting -> LoadingBlock()
            else -> EmptyState(
                icon = Icons.Outlined.HourglassEmpty,
                title = stringResource(R.string.queue_unavailable_title),
                body = stringResource(R.string.queue_unavailable_body),
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                container = MaterialTheme.colorScheme.surfaceContainerHighest,
            )
        }
    }
}

@Composable
private fun CenteredColumn(content: @Composable () -> Unit) {
    Column(
        modifier = Modifier.fillMaxSize(),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) { content() }
}

@Composable
private fun SectionHeader(kind: QueueSectionKind, count: Int, speedLabel: String?, modifier: Modifier = Modifier) {
    val label = stringResource(
        when (kind) {
            QueueSectionKind.Active -> R.string.queue_section_active
            QueueSectionKind.Queued -> R.string.queue_section_queued
            QueueSectionKind.Paused -> R.string.queue_section_paused
        },
    )
    val accent = statusAccent(kind.status).accent
    Row(
        modifier.fillMaxWidth().padding(start = 4.dp, end = 4.dp, top = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Box(Modifier.size(8.dp).clip(CircleShape).background(accent))
        Text(
            label,
            style = MaterialTheme.typography.titleSmall,
            color = MaterialTheme.colorScheme.onSurface,
            modifier = Modifier.semantics { heading() },
        )
        Text(count.toString(), style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
        if (speedLabel != null) {
            Text(
                speedLabel,
                style = MaterialTheme.typography.labelLarge,
                color = accent,
                textAlign = TextAlign.End,
                modifier = Modifier.weight(1f),
            )
        }
    }
}

private val QueueSectionKind.status: JobStatus
    get() = when (this) {
        QueueSectionKind.Active -> JobStatus.DOWNLOADING
        QueueSectionKind.Queued -> JobStatus.PENDING
        QueueSectionKind.Paused -> JobStatus.PAUSED
    }

/** How a job in one state is tinted and which glyph says what it is doing. */
private data class StatusAccent(val accent: Color, val container: Color, val onContainer: Color, val icon: ImageVector)

@Composable
private fun statusAccent(status: JobStatus): StatusAccent {
    val c = MaterialTheme.colorScheme
    val s = MaterialTheme.statusColors
    return when (status) {
        JobStatus.DOWNLOADING -> StatusAccent(c.primary, c.primaryContainer, c.onPrimaryContainer, Icons.Outlined.Download)
        JobStatus.PROCESSING -> StatusAccent(c.tertiary, c.tertiaryContainer, c.onTertiaryContainer, Icons.Outlined.Autorenew)
        JobStatus.PAUSED -> StatusAccent(s.warning, s.warningContainer, s.onWarningContainer, Icons.Outlined.PauseCircle)
        JobStatus.PENDING -> StatusAccent(c.outline, c.surfaceContainerHighest, c.onSurfaceVariant, Icons.Outlined.Schedule)
        JobStatus.COMPLETED -> StatusAccent(s.success, s.successContainer, s.onSuccessContainer, Icons.Outlined.Download)
        JobStatus.FAILED, JobStatus.CANCELLED -> StatusAccent(c.error, c.errorContainer, c.onErrorContainer, Icons.Outlined.Close)
    }
}

private fun layoutIcon(layout: String?): ImageVector = when (layout) {
    "movie" -> Icons.Outlined.Movie
    "tv" -> Icons.Outlined.Tv
    else -> Icons.Outlined.Folder
}

@Composable
private fun JobCard(
    job: JobCardModel,
    onPause: () -> Unit,
    onResume: () -> Unit,
    onCancel: () -> Unit,
    modifier: Modifier = Modifier,
) {
    AppCard(modifier = modifier.fillMaxWidth()) {
        JobDetails(job, onPause, onResume, onCancel)
    }
}

@Composable
private fun JobDetails(
    job: JobCardModel,
    onPause: () -> Unit,
    onResume: () -> Unit,
    onCancel: () -> Unit,
    showCategory: Boolean = true,
) {
    val accent = statusAccent(job.status)
    val colors = MaterialTheme.colorScheme
    val meta = listOfNotNull(
        job.categoryLabel.takeIf { showCategory },
        stringResource(R.string.queue_waiting).takeIf { job.status == JobStatus.PENDING },
    ).joinToString(" · ")
    Column(Modifier.padding(start = 16.dp, end = 4.dp, top = 12.dp, bottom = 14.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Row(horizontalArrangement = Arrangement.spacedBy(14.dp), verticalAlignment = Alignment.Top) {
            IconTile(layoutIcon(job.layout), Modifier.padding(top = 4.dp), container = accent.container, content = accent.onContainer, size = 40.dp)
            Column(Modifier.weight(1f).padding(top = 4.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(job.heading, style = MaterialTheme.typography.titleMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                job.secondary?.let {
                    Text(it, style = MaterialTheme.typography.bodyMedium, color = colors.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
                if (meta.isNotEmpty()) {
                    Text(meta, style = MaterialTheme.typography.labelMedium, color = colors.onSurfaceVariant, maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
            }
            Row(verticalAlignment = Alignment.CenterVertically) { JobActionButtons(job, onPause, onResume, onCancel) }
        }
        val line = job.stats ?: job.statusLabel.takeIf { job.status == JobStatus.PROCESSING }
        if (job.showsProgressBar || line != null) {
            Column(Modifier.padding(end = 12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                if (job.showsProgressBar) ProgressRow(job.progress, job.indeterminate, job.percentLabel, accent.accent)
                line?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = colors.onSurfaceVariant) }
            }
        }
    }
}

/** Pause or Resume as one tap; Cancel sits behind the overflow so a stray touch cannot stop a download. */
@Composable
private fun JobActionButtons(job: JobCardModel, onPause: () -> Unit, onResume: () -> Unit, onCancel: () -> Unit) {
    var menuOpen by remember { mutableStateOf(false) }
    if (JobAction.Pause in job.actions) {
        IconButton(onClick = onPause) {
            Icon(Icons.Outlined.Pause, contentDescription = stringResource(R.string.queue_cd_pause, job.heading))
        }
    }
    if (JobAction.Resume in job.actions) {
        FilledTonalIconButton(onClick = onResume) {
            Icon(Icons.Outlined.PlayArrow, contentDescription = stringResource(R.string.queue_cd_resume, job.heading))
        }
    }
    if (JobAction.Cancel in job.actions) {
        Box {
            IconButton(onClick = { menuOpen = true }) {
                Icon(Icons.Outlined.MoreVert, contentDescription = stringResource(R.string.queue_batch_menu, job.heading))
            }
            DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                DropdownMenuItem(
                    text = { Text(stringResource(R.string.queue_cancel_download), color = MaterialTheme.colorScheme.error) },
                    leadingIcon = { Icon(Icons.Outlined.Close, contentDescription = null, tint = MaterialTheme.colorScheme.error) },
                    onClick = { menuOpen = false; onCancel() },
                )
            }
        }
    }
}

/** Determinate bars animate between updates; an unknown [progress] with [indeterminate] shows the moving bar. */
@Composable
private fun ProgressRow(progress: Float?, indeterminate: Boolean, percentLabel: String?, color: Color, modifier: Modifier = Modifier) {
    val description = if (indeterminate || progress == null) {
        stringResource(R.string.queue_cd_finishing)
    } else {
        val percent = percentInt(progress)
        pluralStringResource(R.plurals.queue_cd_progress, percent, percent)
    }
    val barModifier = Modifier.height(8.dp).clip(CircleShape)
    val track = MaterialTheme.colorScheme.surfaceContainerHighest
    Row(
        modifier.fillMaxWidth().clearAndSetSemantics { contentDescription = description },
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        if (indeterminate || progress == null) {
            LinearProgressIndicator(barModifier.weight(1f), color = color, trackColor = track)
        } else {
            val animated by animateFloatAsState(progress, animationSpec = tween(durationMillis = 500), label = "progress")
            LinearProgressIndicator(
                progress = { animated },
                modifier = barModifier.weight(1f),
                color = color,
                trackColor = track,
                drawStopIndicator = {},
            )
            percentLabel?.let { Text(it, style = MaterialTheme.typography.labelLarge, color = color) }
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
    modifier: Modifier = Modifier,
) {
    var menuOpen by remember { mutableStateOf(false) }
    val toggleLabel = stringResource(if (expanded) R.string.queue_batch_collapse else R.string.queue_batch_expand, group.title)
    val canPause = group.jobs.any { JobAction.Pause in it.actions }
    val canResume = group.jobs.any { JobAction.Resume in it.actions }
    val accent = statusAccent(group.section.status)
    val chevron by animateFloatAsState(if (expanded) 180f else 0f, label = "chevron")

    AppCard(modifier = modifier.fillMaxWidth()) {
        Row(
            Modifier.fillMaxWidth().clickable(onClickLabel = toggleLabel, onClick = onToggleExpanded).padding(start = 16.dp, end = 4.dp, top = 12.dp, bottom = 12.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(14.dp),
        ) {
            IconTile(Icons.Outlined.Layers, container = accent.container, content = accent.onContainer, size = 40.dp)
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Text(group.title, style = MaterialTheme.typography.titleMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                Text(
                    pluralStringResource(R.plurals.queue_episode_count, group.episodeCount, group.episodeCount),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                group.progress?.let { ProgressRow(it, indeterminate = false, percentLabel = group.percentLabel, color = accent.accent) }
            }
            Icon(
                Icons.Outlined.ExpandMore,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.rotate(chevron),
            )
            Box {
                IconButton(onClick = { menuOpen = true }) {
                    Icon(Icons.Outlined.MoreVert, contentDescription = stringResource(R.string.queue_batch_menu, group.title))
                }
                DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                    if (canPause) {
                        DropdownMenuItem(
                            text = { Text(stringResource(R.string.queue_batch_pause_all)) },
                            leadingIcon = { Icon(Icons.Outlined.Pause, contentDescription = null) },
                            onClick = { menuOpen = false; onPauseAll() },
                        )
                    }
                    if (canResume) {
                        DropdownMenuItem(
                            text = { Text(stringResource(R.string.queue_batch_resume_all)) },
                            leadingIcon = { Icon(Icons.Outlined.PlayArrow, contentDescription = null) },
                            onClick = { menuOpen = false; onResumeAll() },
                        )
                    }
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.queue_batch_cancel_all)) },
                        leadingIcon = { Icon(Icons.Outlined.Close, contentDescription = null, tint = MaterialTheme.colorScheme.error) },
                        onClick = { menuOpen = false; onCancelAll() },
                    )
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
        icon = { Icon(Icons.Outlined.Close, contentDescription = null, tint = MaterialTheme.colorScheme.error) },
        title = { Text(title) },
        text = { Text(body) },
        confirmButton = {
            TextButton(
                onClick = onConfirm,
                colors = androidx.compose.material3.ButtonDefaults.textButtonColors(contentColor = MaterialTheme.colorScheme.error),
            ) { Text(stringResource(R.string.queue_cancel_confirm)) }
        },
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
    layout = "tv",
    speed = "3.1 MiB/s",
    eta = "1:52 left",
    size = "463 MiB / 720 MiB",
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
                    AppCard(Modifier.fillMaxWidth().padding(start = 16.dp), color = MaterialTheme.colorScheme.surfaceContainer) {
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
