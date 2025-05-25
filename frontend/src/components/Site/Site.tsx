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
            <div className="block">
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

            <h1>{ site.name }</h1>

            <button
                className="block text-sm text-gray-500 hover:text-gray-700"
                onClick={() => OpenSiteFilesDir(site.id)}
            >
                View site files
                <FolderOpenIcon
                    className="h-5 w-5 text-gray-500 cursor-pointer"
                    
                />
            </button>

            <p>Slug: { site.slug }</p>
            <p>URL: https://{ site.domain }</p>

        </div>
    )
}
