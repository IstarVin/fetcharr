package com.istarvin.fetcharr.ui

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.platform.LocalContext
import androidx.core.content.ContextCompat
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import com.istarvin.fetcharr.data.settings.SettingsRepository
import com.istarvin.fetcharr.notifications.BackgroundController
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import javax.inject.Inject

@HiltViewModel
class NotificationPermissionViewModel @Inject constructor(
    controller: BackgroundController,
    private val settings: SettingsRepository,
) : ViewModel() {
    /** One emission per successful submit. */
    val submits: Flow<Unit> = controller.submitSucceeded

    suspend fun alreadyAsked(): Boolean = settings.notificationPermissionAsked.first()

    fun markAsked() {
        viewModelScope.launch { settings.markNotificationPermissionAsked() }
    }
}

/**
 * Asks for POST_NOTIFICATIONS (Android 13+) right after the first successful submit, once. If the user denies, the
 * app keeps working: downloads and the in-app queue are unaffected, only notifications are missing.
 */
@Composable
fun NotificationPermissionEffect(viewModel: NotificationPermissionViewModel = hiltViewModel()) {
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return
    val context = LocalContext.current
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { }
    LaunchedEffect(viewModel) {
        viewModel.submits.collect {
            val granted = ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) ==
                PackageManager.PERMISSION_GRANTED
            if (!granted && !viewModel.alreadyAsked()) {
                viewModel.markAsked()
                launcher.launch(Manifest.permission.POST_NOTIFICATIONS)
            }
        }
    }
}
