import { useEffect, useState } from 'react';
import type { types } from '../../../wailsjs/go/models';
import { StartSite, StopSite, OpenSiteFilesDir } from '../../../wailsjs/go/sites/SiteManager';
import {
    PlayIcon,
    StopIcon,
    FolderOpenIcon,
    ArrowPathIcon,
} from '@heroicons/react/24/solid';

import { useParams } from 'react-router';

export default function Site({ sites }: { sites: types.Site[] }) {
    let { siteId } = useParams();

    const [started, setStarted] = useState(false);
    const [toggling, setToggling] = useState(false);

    if (!sites) {
        return null;
    }

    const site = sites.find((site) => site.id === siteId);

    const handleStartSite = async (id: string) => {
        setToggling(true);
        await StartSite(id);
        setStarted(true);
        setToggling(false);
    };

    const handleStopSite = async (id: string) => {
        setToggling(true);
        await StopSite(id);
        setStarted(false);
        setToggling(false);
    };

    if (!site) {
        return null;
    }

    return (
        <div className="site">
            <h1>{ site.name }</h1>

            <div className="site_meta">
                { toggling
                    ? (
                        <ArrowPathIcon
                            className="size-5 animate-spin text-gray-500"
                        />
                    ) : started ? (
                        <StopIcon
                            className="size-5 text-gray-500 cursor-pointer"
                            onClick={() => handleStopSite(site.id)}
                        />
                    ) : (
                        <PlayIcon
                            className="size-5 text-gray-500 cursor-pointer"
                            onClick={() => handleStartSite(site.id)}
                        />
                    )
                }
            </div>

            <button
                className="block text-sm text-gray-500 hover:text-gray-700"
                onClick={() => OpenSiteFilesDir(site.id)}
            >
                View site files
                <FolderOpenIcon
                    className="h-5 w-5 text-gray-500 cursor-pointer"
                    
                />
            </button>

            <h3 className="text-lg font-semibold mt-4">Site</h3>
            <p>ID: { site.id }</p>
            <p>Slug: { site.slug }</p>
            <p>URL: <a target="_blank" href={'https://' + site.domain }>https://{ site.domain }</a></p>
            <p>Files Dir: { site.filesDir }</p>
            <p>Public Dir: { site.publicDir }</p>

            <h3 className="text-lg font-semibold mt-4">Versions</h3>
            <p>PHP: { site.phpVersion }</p>
            <p>MySQL: { site.mysqlVersion }</p>
            <p>Redis: { site.redisVersion }</p>

            <h3 className="text-lg font-semibold mt-4">Database</h3>
            <p>Hostname: <code>database</code></p>
            <p>Name: <code>wordpress</code></p>
            <p>User: <code>wordpress</code></p>
            <p>Pass: <code>password</code></p>

        </div>
    )
}
