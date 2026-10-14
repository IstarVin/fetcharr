package dev.istarvin.fetcharr.ui

import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.History
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material3.Icon
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.stringResource
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.navigation.NavDestination.Companion.hasRoute
import androidx.navigation.NavDestination.Companion.hierarchy
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import dev.istarvin.fetcharr.R
import dev.istarvin.fetcharr.ui.add.AddScreen
import dev.istarvin.fetcharr.ui.history.HistoryScreen
import dev.istarvin.fetcharr.ui.navigation.AddRoute
import dev.istarvin.fetcharr.ui.navigation.HistoryRoute
import dev.istarvin.fetcharr.ui.navigation.QueueRoute
import dev.istarvin.fetcharr.ui.navigation.SettingsRoute
import dev.istarvin.fetcharr.ui.queue.QueueScreen
import dev.istarvin.fetcharr.ui.settings.SettingsScreen
import kotlin.reflect.KClass

/**
 * The app-wide snackbar host. Screens call `LocalSnackbarHostState.current.showSnackbar(...)`
 * from a coroutine (for example `rememberCoroutineScope().launch { ... }`).
 */
val LocalSnackbarHostState = staticCompositionLocalOf<SnackbarHostState> {
    error("LocalSnackbarHostState is only available inside FetcharrAppUi")
}

private data class Tab(val route: Any, val routeClass: KClass<*>, val labelRes: Int, val icon: ImageVector)

private val tabs = listOf(
    Tab(QueueRoute, QueueRoute::class, R.string.nav_queue, Icons.Outlined.Download),
    Tab(HistoryRoute, HistoryRoute::class, R.string.nav_history, Icons.Outlined.History),
    Tab(SettingsRoute, SettingsRoute::class, R.string.nav_settings, Icons.Outlined.Settings),
)

@Composable
fun FetcharrAppUi() {
    val navController = rememberNavController()
    val snackbarHostState = remember { SnackbarHostState() }
    val backStackEntry by navController.currentBackStackEntryAsState()
    val destination = backStackEntry?.destination
    val showBottomBar = destination == null || tabs.any { tab -> destination.hierarchy.any { it.hasRoute(tab.routeClass) } }

    CompositionLocalProvider(LocalSnackbarHostState provides snackbarHostState) {
        Scaffold(
            snackbarHost = { SnackbarHost(snackbarHostState) },
            bottomBar = {
                if (showBottomBar) {
                    NavigationBar {
                        tabs.forEach { tab ->
                            val label = stringResource(tab.labelRes)
                            NavigationBarItem(
                                selected = destination?.hierarchy?.any { it.hasRoute(tab.routeClass) } == true,
                                onClick = {
                                    navController.navigate(tab.route) {
                                        popUpTo(navController.graph.findStartDestination().id) { saveState = true }
                                        launchSingleTop = true
                                        restoreState = true
                                    }
                                },
                                icon = { Icon(tab.icon, contentDescription = null) },
                                label = { Text(label) },
                            )
                        }
                    }
                }
            },
        ) { innerPadding ->
            NavHost(
                navController = navController,
                startDestination = QueueRoute,
                modifier = Modifier
                    .padding(innerPadding)
                    .consumeWindowInsets(innerPadding),
            ) {
                composable<QueueRoute> {
                    QueueScreen(
                        onOpenSettings = {
                            navController.navigate(SettingsRoute) {
                                popUpTo(navController.graph.findStartDestination().id) { saveState = true }
                                launchSingleTop = true
                                restoreState = true
                            }
                        },
                    )
                }
                composable<HistoryRoute> { HistoryScreen() }
                composable<SettingsRoute> { SettingsScreen() }
                composable<AddRoute> { AddScreen(onBack = { navController.popBackStack() }) }
            }
        }
    }
}
