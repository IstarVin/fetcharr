package com.istarvin.fetcharr.ui

import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.History
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.History
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material3.Badge
import androidx.compose.material3.BadgedBox
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.NavigationRail
import androidx.compose.material3.NavigationRailItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.navigation.NavDestination.Companion.hasRoute
import androidx.navigation.NavDestination.Companion.hierarchy
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.istarvin.fetcharr.R
import com.istarvin.fetcharr.ui.add.AddScreen
import com.istarvin.fetcharr.ui.history.HistoryScreen
import com.istarvin.fetcharr.ui.navigation.AddRoute
import com.istarvin.fetcharr.ui.navigation.HistoryRoute
import com.istarvin.fetcharr.ui.navigation.OpenTarget
import com.istarvin.fetcharr.ui.navigation.QueueRoute
import com.istarvin.fetcharr.ui.navigation.SettingsRoute
import com.istarvin.fetcharr.ui.incoming.HubEvent
import com.istarvin.fetcharr.ui.incoming.IncomingSheetHost
import com.istarvin.fetcharr.ui.incoming.IncomingViewModel
import com.istarvin.fetcharr.ui.queue.QueueScreen
import com.istarvin.fetcharr.ui.settings.SettingsScreen
import com.istarvin.fetcharr.ui.settings.ServerSettingsScreen
import com.istarvin.fetcharr.ui.navigation.ServerSettingsRoute
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.launch
import kotlin.reflect.KClass

/**
 * The app-wide snackbar host. Screens call `LocalSnackbarHostState.current.showSnackbar(...)`
 * from a coroutine (for example `rememberCoroutineScope().launch { ... }`).
 */
val LocalSnackbarHostState = staticCompositionLocalOf<SnackbarHostState> {
    error("LocalSnackbarHostState is only available inside FetcharrAppUi")
}

/** The tab icon; the Queue tab carries a badge with the number of downloads running or waiting. */
@Composable
private fun TabIcon(tab: Tab, selected: Boolean, badge: Int) {
    val icon: @Composable () -> Unit = { Icon(if (selected) tab.selectedIcon else tab.icon, contentDescription = null) }
    if (badge > 0 && tab.routeClass == QueueRoute::class) {
        BadgedBox(badge = { Badge(containerColor = MaterialTheme.colorScheme.primary, contentColor = MaterialTheme.colorScheme.onPrimary) { Text(if (badge > 99) "99+" else badge.toString()) } }) { icon() }
    } else {
        icon()
    }
}

private data class Tab(val route: Any, val routeClass: KClass<*>, val labelRes: Int, val icon: ImageVector, val selectedIcon: ImageVector)

private val tabs = listOf(
    Tab(QueueRoute, QueueRoute::class, R.string.nav_queue, Icons.Outlined.Download, Icons.Filled.Download),
    Tab(HistoryRoute, HistoryRoute::class, R.string.nav_history, Icons.Outlined.History, Icons.Filled.History),
    Tab(SettingsRoute, SettingsRoute::class, R.string.nav_settings, Icons.Outlined.Settings, Icons.Filled.Settings),
)

@Composable
fun FetcharrAppUi(openTargets: Flow<OpenTarget> = emptyFlow()) {
    val navController = rememberNavController()
    val snackbarHostState = remember { SnackbarHostState() }
    val backStackEntry by navController.currentBackStackEntryAsState()
    val destination = backStackEntry?.destination
    val onSettingsDestination = destination?.hierarchy?.any {
        it.hasRoute(SettingsRoute::class) || it.hasRoute(ServerSettingsRoute::class)
    } == true
    var recoveringIncomingRequest by rememberSaveable { mutableStateOf(false) }
    LaunchedEffect(onSettingsDestination) {
        if (!onSettingsDestination) recoveringIncomingRequest = false
    }
    val incomingViewModel: IncomingViewModel = hiltViewModel()
    val queueCount by hiltViewModel<ShellViewModel>().queueCount.collectAsStateWithLifecycle()
    val openSettings = {
        navController.navigate(SettingsRoute) {
            popUpTo(navController.graph.findStartDestination().id) { saveState = true }
            launchSingleTop = true
            restoreState = true
        }
    }

    // Snackbars and "open Add" requests from intents and from the Incoming sheet.
    LaunchedEffect(incomingViewModel) {
        incomingViewModel.events.collect { event ->
            when (event) {
                is HubEvent.Snackbar -> {
                    snackbarHostState.currentSnackbarData?.dismiss()
                    launch { snackbarHostState.showSnackbar(event.message) }
                }
                HubEvent.OpenSettings -> openSettings()
                is HubEvent.OpenAdd -> {
                    val onAdd = navController.currentBackStackEntry?.destination?.hasRoute(AddRoute::class) == true
                    when {
                        // Add is already open: a new share replaces its URL field, other fields are kept.
                        onAdd -> event.url?.let(incomingViewModel::replaceAddUrl)
                        else -> navController.navigate(AddRoute(event.url)) { launchSingleTop = true }
                    }
                }
            }
        }
    }

    // Notification taps ("open Queue" / "open History").
    LaunchedEffect(openTargets) {
        openTargets.collect { target ->
            navController.navigate(if (target == OpenTarget.HISTORY) HistoryRoute else QueueRoute) {
                popUpTo(navController.graph.findStartDestination().id) { saveState = true }
                launchSingleTop = true
                restoreState = true
            }
        }
    }

    val showNavigation = destination == null || tabs.any { tab -> destination.hierarchy.any { it.hasRoute(tab.routeClass) } }
    val selectTab: (Tab) -> Unit = { tab ->
        navController.navigate(tab.route) {
            popUpTo(navController.graph.findStartDestination().id) { saveState = true }
            launchSingleTop = true
            restoreState = true
        }
    }

    NotificationPermissionEffect()

    CompositionLocalProvider(LocalSnackbarHostState provides snackbarHostState) {
        BoxWithConstraints(Modifier.fillMaxSize()) {
            val useRail = maxWidth >= 600.dp
            Scaffold(
                snackbarHost = { SnackbarHost(snackbarHostState) },
                bottomBar = {
                    if (showNavigation && !useRail) {
                        NavigationBar {
                            tabs.forEach { tab ->
                                val label = stringResource(tab.labelRes)
                                val selected = destination?.hierarchy?.any { it.hasRoute(tab.routeClass) } == true
                                NavigationBarItem(
                                    selected = selected,
                                    onClick = { selectTab(tab) },
                                    icon = { TabIcon(tab, selected, queueCount) },
                                    label = { Text(label) },
                                )
                            }
                        }
                    }
                },
            ) { innerPadding ->
                Row(Modifier.fillMaxSize().padding(innerPadding).consumeWindowInsets(innerPadding)) {
                    if (showNavigation && useRail) {
                        NavigationRail {
                            tabs.forEach { tab ->
                                val selected = destination?.hierarchy?.any { it.hasRoute(tab.routeClass) } == true
                                NavigationRailItem(
                                    selected = selected,
                                    onClick = { selectTab(tab) },
                                    icon = { TabIcon(tab, selected, queueCount) },
                                    label = { Text(stringResource(tab.labelRes)) },
                                )
                            }
                        }
                    }
                    NavHost(
                        navController = navController,
                        startDestination = QueueRoute,
                        modifier = Modifier.weight(1f).fillMaxSize(),
                    ) {
                        composable<QueueRoute> {
                            QueueScreen(
                                onOpenSettings = { openSettings() },
                                onAdd = { navController.navigate(AddRoute()) },
                            )
                        }
                        composable<HistoryRoute> { HistoryScreen(onOpenSettings = { openSettings() }) }
                        composable<SettingsRoute> {
                            SettingsScreen(onServerSettings = { navController.navigate(ServerSettingsRoute) })
                        }
                        composable<ServerSettingsRoute> {
                            ServerSettingsScreen(onBack = { navController.popBackStack() })
                        }
                        composable<AddRoute> {
                            AddScreen(
                                onBack = { navController.popBackStack() },
                                onOpenSettings = { openSettings() },
                                onOpenServerSettings = { navController.navigate(ServerSettingsRoute) },
                            )
                        }
                    }
                }
            }
        }
        IncomingSheetHost(
            onOpenSettings = { recoveringIncomingRequest = true; openSettings() },
            onSettingsDestination = onSettingsDestination,
            recoveringInSettings = recoveringIncomingRequest,
            viewModel = incomingViewModel,
        )
    }
}
