// External Dependencies.
import { useState, useEffect } from 'react';
import { BrowserRouter as Router, Routes, Route, Link } from 'react-router';

// Internal Dependencies.
import NewSiteModal from './components/modals/NewSiteModal';
import Sites from './components/Sites';
import Site from './components/Site';

import { EventsOn, EventsOff } from '../wailsjs/runtime';
import type { types } from '../wailsjs/go/models';
import { GetSites, AddSite, DeleteSite } from '../wailsjs/go/sites/SiteManager';

import { Tab, TabGroup, TabList, TabPanel, TabPanels } from '@headlessui/react'

function App() {
	const [sites, setSites] = useState<types.Site[]>([]);

	useEffect(() => {
		GetSites().then(setSites);

		EventsOn("sitesUpdated", (sites) => {
			setSites(sites);
		});

		return () => EventsOff("sitesUpdated");
	}, []);

	return (
		<Router>
			<div className="flex h-screen text-black">
				<aside className="w-64 bg-gray-900 px-4 py-6 flex flex-col">
					<h1 className="text-2xl font-bold text-white mb-4">Locorum</h1>
					<h2 className="text-gray-100 text-lg mb-4">Sites</h2>
					<Sites sites={sites} deleteSite={DeleteSite} />
					<div className="mt-4"></div>
					<NewSiteModal addSite={AddSite} />
				</aside>

				<main className="page-content flex-1 bg-white p-8 overflow-y-auto">
					<div>
						{/* <TabGroup>
							<TabList>
								<Tab className="data-hover:underline data-selected:bg-blue-500 data-selected:text-white">Tab 1</Tab>
								<Tab className="data-hover:underline data-selected:bg-blue-500 data-selected:text-white">Tab 2</Tab>
								<Tab className="data-hover:underline data-selected:bg-blue-500 data-selected:text-white">Tab 3</Tab>
							</TabList>
							<TabPanels>
								<TabPanel>Content 1</TabPanel>
								<TabPanel>Content 2</TabPanel>
								<TabPanel>Content 3</TabPanel>
							</TabPanels>
						</TabGroup> */}

						<Routes>
							<Route path="/" element={<>Home Page</>} />
							<Route path="sites/:siteId" element={<Site sites={sites} />} />
						</Routes>
					</div>
				</main>
			</div>
		</Router>
	)
}

export default App
