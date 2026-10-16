package com.istarvin.fetcharr.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.istarvin.fetcharr.data.network.dto.JobStatus
import com.istarvin.fetcharr.data.sync.ActiveJobsStore
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn

/** What the navigation bar needs to know about the queue: how many downloads are running or waiting. */
@HiltViewModel
class ShellViewModel @Inject constructor(store: ActiveJobsStore) : ViewModel() {
    val queueCount: StateFlow<Int> = store.jobs
        .map { jobs -> jobs.count { it.status != JobStatus.PAUSED } }
        .distinctUntilChanged()
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), 0)
}
